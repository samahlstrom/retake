//! Expressions in an embedded, sandboxed QuickJS (spec/timeline.md, "Expressions").
//!
//! An expression sees only `t`, `value` and `props`. There is no I/O (QuickJS without its std/os modules), no `Date`,
//! `Math.random` throws, and every call has a time budget and the runtime a memory limit. Same `t`, same result, in
//! any order: that's what lets graphics be cached by their inputs.

use retake_timeline::EditError;
use retake_timeline::keyframe::ExprEval;
use rquickjs::{Context, Ctx, Function, Persistent, Runtime, Value as JsValue};
use serde_json::Value;
use std::collections::HashMap;
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::Instant;

const MEMORY_LIMIT: usize = 32 << 20;
const STACK_LIMIT: usize = 512 << 10;
const CALL_BUDGET_MS: u64 = 100;

const PRELUDE: &str = r#"
"use strict";
delete globalThis.Date;
delete globalThis.eval;
Math.random = function () { throw new Error("Math.random is not allowed in expressions (not deterministic)"); };
Object.freeze(Math);
Object.defineProperty(globalThis, "__deepFreeze", { value: function f(o) {
  if (o !== null && typeof o === "object" && !Object.isFrozen(o)) {
    Object.freeze(o);
    for (const k of Object.keys(o)) f(o[k]);
  }
  return o;
} });
Object.freeze(globalThis);
"#;

/// A newly compiled function and newly parsed props, to keep for the next call.
type Saved = (Option<Persistent<Function<'static>>>, Option<Persistent<JsValue<'static>>>);

pub struct Js {
    // Field order matters: Rust drops fields in order, and every JS value must go before the context and runtime
    // (QuickJS aborts if objects are still alive when its runtime is freed).
    fns: HashMap<String, Persistent<Function<'static>>>,
    props_cache: Option<(String, Persistent<JsValue<'static>>)>,
    ctx: Context,
    rt: Runtime,
    epoch: Instant,
    deadline_us: Arc<AtomicU64>,
}

impl Js {
    pub fn new() -> Result<Js, EditError> {
        let e = |x: rquickjs::Error| EditError::new("expr_engine", format!("QuickJS: {x}"));
        let rt = Runtime::new().map_err(e)?;
        rt.set_memory_limit(MEMORY_LIMIT);
        rt.set_max_stack_size(STACK_LIMIT);
        let epoch = Instant::now();
        let deadline_us = Arc::new(AtomicU64::new(u64::MAX));
        let d = deadline_us.clone();
        rt.set_interrupt_handler(Some(Box::new(move || {
            epoch.elapsed().as_micros() as u64 > d.load(Ordering::Relaxed)
        })));
        let ctx = Context::full(&rt).map_err(e)?;
        ctx.with(|c| c.eval::<(), _>(PRELUDE)).map_err(e)?;
        Ok(Js { fns: HashMap::new(), props_cache: None, ctx, rt, epoch, deadline_us })
    }

    fn arm(&self) {
        let now = self.epoch.elapsed().as_micros() as u64;
        self.deadline_us.store(now + CALL_BUDGET_MS * 1000, Ordering::Relaxed);
    }

    fn disarm(&self) {
        self.deadline_us.store(u64::MAX, Ordering::Relaxed);
    }

    /// Runs `expr` and hands the result to `conv` inside the context.
    fn call<R>(
        &mut self,
        expr: &str,
        t: f64,
        value: Option<&[f64]>,
        props: &Value,
        conv: impl for<'js> FnOnce(&Ctx<'js>, JsValue<'js>) -> Result<R, String>,
    ) -> Result<R, EditError> {
        let fail = |msg: String| EditError::new("expr_error", format!("expression `{expr}`: {msg}"));
        let props_json = props.to_string();
        let compiled = self.fns.get(expr).cloned();
        let cached_props = self.props_cache.as_ref().filter(|(k, _)| *k == props_json).map(|(_, v)| v.clone());
        self.arm();
        let result = self.ctx.with(|ctx| -> Result<(R, Saved), String> {
            let describe = |e: rquickjs::Error| -> String {
                if e.is_exception() {
                    let ex = ctx.catch();
                    if let Some(o) = ex.as_exception() {
                        return o.message().unwrap_or_else(|| "exception".into());
                    }
                    return format!("{ex:?}");
                }
                e.to_string()
            };
            let (f, new_f) = match compiled {
                Some(p) => (p.restore(&ctx).map_err(describe)?, None),
                None => {
                    let src = format!("(function (t, value, props) {{ \"use strict\"; return ({expr}\n); }})");
                    let f: Function = ctx.eval(src).map_err(describe)?;
                    let saved = Persistent::save(&ctx, f.clone());
                    (f, Some(saved))
                }
            };
            let (pv, new_p) = match cached_props {
                Some(p) => (p.restore(&ctx).map_err(describe)?, None),
                None => {
                    let v: JsValue = ctx.json_parse(props_json.clone()).map_err(describe)?;
                    let freeze: Function = ctx.globals().get("__deepFreeze").map_err(describe)?;
                    let v: JsValue = freeze.call((v,)).map_err(describe)?;
                    let saved = Persistent::save(&ctx, v.clone());
                    (v, Some(saved))
                }
            };
            let vv: JsValue = match value {
                None => JsValue::new_undefined(ctx.clone()),
                Some([x]) => JsValue::new_number(ctx.clone(), *x),
                Some(xs) => {
                    let arr = rquickjs::Array::new(ctx.clone()).map_err(describe)?;
                    for (i, x) in xs.iter().enumerate() {
                        arr.set(i, *x).map_err(describe)?;
                    }
                    arr.into_value()
                }
            };
            let out: JsValue = f.call((t, vv, pv)).map_err(describe)?;
            let r = conv(&ctx, out)?;
            Ok((r, (new_f, new_p)))
        });
        self.disarm();
        let (r, (new_f, new_p)) = result.map_err(fail)?;
        if let Some(f) = new_f {
            self.fns.insert(expr.to_string(), f);
        }
        if let Some(p) = new_p {
            self.props_cache = Some((props_json, p));
        }
        Ok(r)
    }

    /// Memory the runtime uses, for diagnostics.
    pub fn memory_used(&self) -> i64 {
        self.rt.memory_usage().memory_used_size
    }
}

impl ExprEval for Js {
    fn eval_num(&mut self, expr: &str, t: f64, value: Option<&[f64]>, props: &Value) -> Result<Vec<f64>, EditError> {
        self.call(expr, t, value, props, |_, v| {
            if let Some(n) = v.as_number() {
                return Ok(vec![n]);
            }
            if let Some(a) = v.as_array() {
                let mut out = vec![];
                for x in a.iter::<JsValue>() {
                    let x = x.map_err(|e| e.to_string())?;
                    out.push(x.as_number().ok_or("arrays must hold numbers")?);
                }
                return Ok(out);
            }
            Err(format!("must return a number or an array of numbers, got {}", v.type_name()))
        })
        .and_then(|v| {
            if v.iter().all(|x| x.is_finite()) {
                Ok(v)
            } else {
                Err(EditError::new("expr_error", format!("expression `{expr}` returned a non-finite number at t={t}")))
            }
        })
    }

    fn eval_text(&mut self, expr: &str, t: f64, props: &Value) -> Result<String, EditError> {
        self.call(expr, t, None, props, |_, v| {
            if let Some(s) = v.as_string() {
                return s.to_string().map_err(|e| e.to_string());
            }
            if let Some(n) = v.as_number() {
                return Ok(if n.fract() == 0.0 && n.abs() < 1e15 { format!("{}", n as i64) } else { format!("{n}") });
            }
            Err(format!("text expressions must return a string, got {}", v.type_name()))
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn evaluates_numbers_arrays_and_text() {
        let mut js = Js::new().unwrap();
        let p = json!({"amp": 2, "label": "GO"});
        assert_eq!(js.eval_num("t * 2", 1.5, None, &p).unwrap(), vec![3.0]);
        assert_eq!(js.eval_num("value * props.amp", 0.0, Some(&[3.0]), &p).unwrap(), vec![6.0]);
        assert_eq!(js.eval_num("[value[0] + t, value[1]]", 1.0, Some(&[10.0, 20.0]), &p).unwrap(), vec![11.0, 20.0]);
        assert_eq!(js.eval_text("props.label + ' ' + Math.floor(t * 10)", 0.25, &p).unwrap(), "GO 2");
        assert_eq!(js.eval_text("Math.floor(t)", 3.7, &p).unwrap(), "3");
    }

    #[test]
    fn deterministic_in_any_order() {
        let exprs = ["Math.sin(t * 6.283) * 5 + value", "Math.pow(t, 1.7) + Math.cos(t) * props.k"];
        let ts: Vec<f64> = (0..200).map(|i| i as f64 / 60.0).collect();
        let p = json!({"k": 3.25});
        let run = |order: &[usize]| {
            let mut js = Js::new().unwrap();
            let mut out = vec![vec![]; ts.len()];
            for &i in order {
                for e in exprs {
                    out[i].extend(js.eval_num(e, ts[i], Some(&[1.0]), &p).unwrap());
                }
            }
            out
        };
        let fwd: Vec<usize> = (0..ts.len()).collect();
        // A fixed shuffle (no RNG needed): stride through the indices.
        let shuffled: Vec<usize> = (0..ts.len()).map(|i| (i * 37 + 11) % ts.len()).collect();
        let a = run(&fwd);
        let b = run(&shuffled);
        for (x, y) in a.iter().zip(&b) {
            let xb: Vec<u64> = x.iter().map(|v| v.to_bits()).collect();
            let yb: Vec<u64> = y.iter().map(|v| v.to_bits()).collect();
            assert_eq!(xb, yb, "bit-identical whatever the order");
        }
    }

    #[test]
    fn sandbox_blocks_nondeterminism_and_io() {
        let mut js = Js::new().unwrap();
        let p = json!({});
        let e = js.eval_num("Math.random()", 0.0, None, &p).unwrap_err();
        assert!(e.message.contains("Math.random is not allowed"), "{e:?}");
        assert!(js.eval_num("Date.now()", 0.0, None, &p).is_err());
        assert!(
            js.eval_num(
                "typeof require === 'undefined' && typeof std === 'undefined' && typeof os === 'undefined' ? 1 : 0",
                0.0,
                None,
                &p
            )
            .unwrap()
                == vec![1.0]
        );
        assert!(js.eval_num("eval('1')", 0.0, None, &p).is_err());
        assert!(js.eval_num("(Math.random = () => 4, 1)", 0.0, None, &p).is_err(), "Math is frozen");
        // Expressions can't keep state between calls: globals and props are frozen.
        assert!(js.eval_num("(globalThis.x = 1, 1)", 0.0, None, &p).is_err());
        let q = json!({"k": {"n": 1}});
        assert!(js.eval_num("(props.k.n = 2, 1)", 0.0, None, &q).is_err());
        assert_eq!(js.eval_num("props.k.n", 0.0, None, &q).unwrap(), vec![1.0]);
    }

    #[test]
    fn runaway_and_bad_results_are_errors() {
        let mut js = Js::new().unwrap();
        let p = json!({});
        let e = js.eval_num("(() => { while (true) {} })()", 0.0, None, &p).unwrap_err();
        assert_eq!(e.code, "expr_error");
        // The engine still works after an interrupt.
        assert_eq!(js.eval_num("1 + 1", 0.0, None, &p).unwrap(), vec![2.0]);
        assert!(js.eval_num("'nope'", 0.0, None, &p).is_err());
        assert!(js.eval_num("1 / 0", 0.0, None, &p).is_err());
        assert!(js.eval_num("syntax error here", 0.0, None, &p).is_err());
        let big = js.eval_num("new Array(1e8).fill(1).length", 0.0, None, &p);
        assert!(big.is_err(), "memory limit");
        assert!(js.memory_used() > 0);
    }
}
