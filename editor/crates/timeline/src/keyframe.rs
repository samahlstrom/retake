//! Props, keyframes and easing (spec/timeline.md, "Props, keyframes and easing").
//!
//! A prop evaluates to a vector of numbers (a scalar is a vector of one). Expressions are delegated to an
//! [`ExprEval`] so this crate stays free of the JavaScript engine.

use crate::error::EditError;
use serde::{Deserialize, Serialize};
use serde_json::Value;

/// A constant prop value: a number or a short array of numbers.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(untagged)]
pub enum PValue {
    Num(f64),
    Arr(Vec<f64>),
}

impl PValue {
    pub fn to_vec(&self) -> Vec<f64> {
        match self {
            PValue::Num(n) => vec![*n],
            PValue::Arr(a) => a.clone(),
        }
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(untagged)]
pub enum Ease {
    Named(String),
    Bezier([f64; 4]),
}

/// A keyframe after resolution: `t` is composition seconds.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Key {
    pub t: f64,
    pub v: PValue,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub ease: Option<Ease>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(untagged)]
pub enum Prop {
    Const(PValue),
    Keys {
        keys: Vec<Key>,
        #[serde(default, skip_serializing_if = "Option::is_none")]
        expr: Option<String>,
    },
    Expr {
        #[serde(default, skip_serializing_if = "Option::is_none")]
        value: Option<PValue>,
        expr: String,
    },
}

/// Evaluates expressions for props. The render crate implements it with QuickJS.
pub trait ExprEval {
    /// Evaluates `expr` with `t`, `value` and `props` bound; returns the numbers it produced.
    fn eval_num(&mut self, expr: &str, t: f64, value: Option<&[f64]>, props: &Value) -> Result<Vec<f64>, EditError>;
    /// Evaluates a text expression; returns the string it produced.
    fn eval_text(&mut self, expr: &str, t: f64, props: &Value) -> Result<String, EditError>;
}

/// An [`ExprEval`] that refuses every expression (for callers that have no engine).
pub struct NoExpr;

impl ExprEval for NoExpr {
    fn eval_num(&mut self, expr: &str, _: f64, _: Option<&[f64]>, _: &Value) -> Result<Vec<f64>, EditError> {
        Err(EditError::new("expr_unavailable", format!("no expression engine to run `{expr}`")))
    }
    fn eval_text(&mut self, expr: &str, _: f64, _: &Value) -> Result<String, EditError> {
        Err(EditError::new("expr_unavailable", format!("no expression engine to run `{expr}`")))
    }
}

impl Prop {
    /// The value at composition time `t`, expressions included.
    pub fn eval(&self, t: f64, props: &Value, ex: &mut dyn ExprEval) -> Result<Vec<f64>, EditError> {
        match self {
            Prop::Const(v) => Ok(v.to_vec()),
            Prop::Keys { keys, expr } => {
                let v = eval_keys(keys, t);
                match expr {
                    Some(e) => ex.eval_num(e, t, Some(&v), props),
                    None => Ok(v),
                }
            }
            Prop::Expr { value, expr } => {
                let v = value.as_ref().map(|v| v.to_vec());
                ex.eval_num(expr, t, v.as_deref(), props)
            }
        }
    }

    /// The value at `t` ignoring expressions (keyframes and constants only).
    pub fn base(&self, t: f64) -> Option<Vec<f64>> {
        match self {
            Prop::Const(v) => Some(v.to_vec()),
            Prop::Keys { keys, .. } => Some(eval_keys(keys, t)),
            Prop::Expr { value, .. } => value.as_ref().map(|v| v.to_vec()),
        }
    }

    pub fn has_expr(&self) -> bool {
        matches!(self, Prop::Keys { expr: Some(_), .. } | Prop::Expr { .. })
    }

    /// True when the prop can't change over time (a constant and no expression).
    pub fn is_static(&self) -> bool {
        match self {
            Prop::Const(_) => true,
            Prop::Keys { keys, expr } => expr.is_none() && keys.windows(2).all(|w| w[0].v == w[1].v),
            Prop::Expr { .. } => false,
        }
    }
}

/// Interpolates keyframes at `t`. Keys are assumed sorted by time (validation checks it).
pub fn eval_keys(keys: &[Key], t: f64) -> Vec<f64> {
    let Some(first) = keys.first() else { return vec![] };
    if t <= first.t || keys.len() == 1 {
        return first.v.to_vec();
    }
    let last = &keys[keys.len() - 1];
    if t >= last.t {
        return last.v.to_vec();
    }
    // The segment [k, k+1) that contains t.
    let k = keys.windows(2).position(|w| t >= w[0].t && t < w[1].t).unwrap_or(keys.len() - 2);
    let (a, b) = (&keys[k], &keys[k + 1]);
    let span = b.t - a.t;
    let x = if span > 0.0 { (t - a.t) / span } else { 1.0 };
    let e = match &a.ease {
        Some(e) => ease(e, x),
        None => x,
    };
    let (va, vb) = (a.v.to_vec(), b.v.to_vec());
    va.iter().zip(vb.iter()).map(|(p, q)| p + (q - p) * e).collect()
}

pub const EASE_NAMES: &[&str] = &[
    "linear",
    "hold",
    "in-quad",
    "out-quad",
    "in-out-quad",
    "in-cubic",
    "out-cubic",
    "in-out-cubic",
    "in-out-sine",
    "smoothstep",
    "in-back",
    "out-back",
    "in-out-back",
    "out-expo",
];

/// Maps progress `x` in [0, 1] through an easing curve.
pub fn ease(e: &Ease, x: f64) -> f64 {
    let x = x.clamp(0.0, 1.0);
    match e {
        Ease::Bezier([x1, y1, x2, y2]) => cubic_bezier(*x1, *y1, *x2, *y2, x),
        Ease::Named(n) => named_ease(n, x),
    }
}

fn named_ease(name: &str, x: f64) -> f64 {
    const C1: f64 = 1.70158;
    const C2: f64 = C1 * 1.525;
    const C3: f64 = C1 + 1.0;
    match name {
        "hold" => 0.0,
        "in-quad" => x * x,
        "out-quad" => 1.0 - (1.0 - x) * (1.0 - x),
        "in-out-quad" => {
            if x < 0.5 {
                2.0 * x * x
            } else {
                1.0 - (-2.0 * x + 2.0).powi(2) / 2.0
            }
        }
        "in-cubic" => x * x * x,
        "out-cubic" => 1.0 - (1.0 - x).powi(3),
        "in-out-cubic" => {
            if x < 0.5 {
                4.0 * x * x * x
            } else {
                1.0 - (-2.0 * x + 2.0).powi(3) / 2.0
            }
        }
        "in-out-sine" => -((std::f64::consts::PI * x).cos() - 1.0) / 2.0,
        "smoothstep" => x * x * (3.0 - 2.0 * x),
        "in-back" => C3 * x * x * x - C1 * x * x,
        "out-back" => 1.0 + C3 * (x - 1.0).powi(3) + C1 * (x - 1.0).powi(2),
        "in-out-back" => {
            if x < 0.5 {
                ((2.0 * x).powi(2) * ((C2 + 1.0) * 2.0 * x - C2)) / 2.0
            } else {
                ((2.0 * x - 2.0).powi(2) * ((C2 + 1.0) * (x * 2.0 - 2.0) + C2) + 2.0) / 2.0
            }
        }
        "out-expo" => {
            if x >= 1.0 {
                1.0
            } else {
                1.0 - 2f64.powf(-10.0 * x)
            }
        }
        _ => x, // "linear" and anything validation let through
    }
}

/// CSS `cubic-bezier(x1, y1, x2, y2)` at progress `x`: solve bx(s) = x for s, return by(s).
pub fn cubic_bezier(x1: f64, y1: f64, x2: f64, y2: f64, x: f64) -> f64 {
    if x <= 0.0 {
        return 0.0;
    }
    if x >= 1.0 {
        return 1.0;
    }
    let bez = |a: f64, b: f64, s: f64| {
        let u = 1.0 - s;
        3.0 * u * u * s * a + 3.0 * u * s * s * b + s * s * s
    };
    let dbez = |a: f64, b: f64, s: f64| {
        let u = 1.0 - s;
        3.0 * u * u * a + 6.0 * u * s * (b - a) + 3.0 * s * s * (1.0 - b)
    };
    // Newton first, bisection as the fallback: deterministic and accurate to ~1e-9.
    let mut s = x;
    for _ in 0..8 {
        let err = bez(x1, x2, s) - x;
        if err.abs() < 1e-10 {
            return bez(y1, y2, s);
        }
        let d = dbez(x1, x2, s);
        if d.abs() < 1e-9 {
            break;
        }
        s = (s - err / d).clamp(0.0, 1.0);
    }
    let (mut lo, mut hi) = (0.0, 1.0);
    s = x;
    for _ in 0..60 {
        let v = bez(x1, x2, s);
        if (v - x).abs() < 1e-10 {
            break;
        }
        if v < x {
            lo = s;
        } else {
            hi = s;
        }
        s = (lo + hi) / 2.0;
    }
    bez(y1, y2, s)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn approx(a: f64, b: f64) -> bool {
        (a - b).abs() < 1e-6
    }

    #[test]
    fn named_eases_hit_their_ends() {
        for n in EASE_NAMES {
            let e = Ease::Named(n.to_string());
            if *n != "hold" {
                assert!(approx(ease(&e, 0.0), 0.0), "{n} at 0");
                assert!(approx(ease(&e, 1.0), 1.0), "{n} at 1");
            }
        }
        assert_eq!(ease(&Ease::Named("hold".into()), 0.99), 0.0);
    }

    #[test]
    fn named_eases_known_values() {
        let e = |n: &str, x: f64| ease(&Ease::Named(n.into()), x);
        assert!(approx(e("linear", 0.25), 0.25));
        assert!(approx(e("in-quad", 0.5), 0.25));
        assert!(approx(e("out-quad", 0.5), 0.75));
        assert!(approx(e("in-out-cubic", 0.5), 0.5));
        assert!(approx(e("smoothstep", 0.5), 0.5));
        assert!(approx(e("in-out-sine", 0.5), 0.5));
        assert!(e("out-back", 0.6) > 1.0, "out-back overshoots");
        assert!(e("in-back", 0.2) < 0.0, "in-back undershoots");
        assert!(approx(e("out-expo", 0.1), 1.0 - 0.5));
    }

    #[test]
    fn bezier_matches_css_curves() {
        // CSS `ease` = cubic-bezier(0.25, 0.1, 0.25, 1): known value at x = 0.5 is ~0.8024.
        let v = cubic_bezier(0.25, 0.1, 0.25, 1.0, 0.5);
        assert!((v - 0.8024).abs() < 1e-3, "{v}");
        // The diagonal is linear.
        for i in 0..=10 {
            let x = i as f64 / 10.0;
            assert!(approx(cubic_bezier(0.0, 0.0, 1.0, 1.0, x), x));
        }
        // Monotone in x for a valid curve.
        let mut prev = -1.0;
        for i in 0..=100 {
            let y = cubic_bezier(0.42, 0.0, 0.58, 1.0, i as f64 / 100.0);
            assert!(y >= prev - 1e-12);
            prev = y;
        }
    }

    #[test]
    fn keys_interpolate_with_ease_on_the_leaving_key() {
        let p: Prop = serde_json::from_value(json!({"keys": [
            {"t": 1.0, "v": 0.0, "ease": "in-quad"},
            {"t": 2.0, "v": 10.0},
            {"t": 4.0, "v": 20.0}
        ]}))
        .unwrap();
        let b = |t: f64| p.base(t).unwrap()[0];
        assert_eq!(b(0.0), 0.0, "before the first key: hold");
        assert!(approx(b(1.5), 2.5), "in-quad from key 0: 10 * 0.25");
        assert!(approx(b(3.0), 15.0), "linear from key 1");
        assert_eq!(b(9.0), 20.0, "after the last key: hold");
    }

    #[test]
    fn vector_keys_interpolate_per_component() {
        let p: Prop = serde_json::from_value(json!({"keys": [
            {"t": 0, "v": [0, 100]}, {"t": 1, "v": [10, 0]}
        ]}))
        .unwrap();
        assert_eq!(p.base(0.5).unwrap(), vec![5.0, 50.0]);
    }

    #[test]
    fn hold_ease_steps() {
        let p: Prop = serde_json::from_value(json!({"keys": [
            {"t": 0, "v": 1, "ease": "hold"}, {"t": 1, "v": 2}
        ]}))
        .unwrap();
        assert_eq!(p.base(0.999).unwrap(), vec![1.0]);
        assert_eq!(p.base(1.0).unwrap(), vec![2.0]);
    }

    #[test]
    fn prop_forms_parse() {
        let c: Prop = serde_json::from_value(json!(3)).unwrap();
        assert!(c.is_static());
        let a: Prop = serde_json::from_value(json!([1, 2])).unwrap();
        assert_eq!(a.base(0.0).unwrap(), vec![1.0, 2.0]);
        let e: Prop = serde_json::from_value(json!({"expr": "t"})).unwrap();
        assert!(e.has_expr() && !e.is_static());
        let ve: Prop = serde_json::from_value(json!({"value": 2, "expr": "value * 2"})).unwrap();
        assert_eq!(ve.base(0.0).unwrap(), vec![2.0]);
        let ke: Prop = serde_json::from_value(json!({"keys": [{"t": 0, "v": 1}], "expr": "value"})).unwrap();
        assert!(ke.has_expr());
        assert!(NoExpr.eval_num("t", 0.0, None, &json!({})).is_err());
    }
}
