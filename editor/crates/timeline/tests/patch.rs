//! Patch apply, log, revert and conflicts, on a timeline file in a scratch folder.

use retake_timeline::Project;
use retake_timeline::patch::{apply, commit, read_log, revert_patch};
use serde_json::{Value, json};
use std::path::{Path, PathBuf};

fn setup(name: &str) -> PathBuf {
    let d = PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("patch").join(name);
    let _ = std::fs::remove_dir_all(&d);
    std::fs::create_dir_all(&d).unwrap();
    let words = json!({"words": [
        {"w": " One", "s": 0.2, "e": 0.5}, {"w": " two", "s": 0.7, "e": 1.0}, {"w": " three", "s": 1.2, "e": 1.6}
    ]});
    std::fs::write(d.join("vo.words.json"), words.to_string()).unwrap();
    std::fs::write(d.join("vo.wav"), b"").unwrap();
    let tl = json!({
        "schema": "timeline/0.1", "id": "p", "rev": 0,
        "format": {"size": [320, 180], "fps": 25},
        "assets": {"proj:vo": {"path": "vo.wav", "words": "vo.words.json"}},
        "spine": [{"id": "a", "asset": "proj:vo", "in": 0, "out": 2}],
        "scenes": [{"id": "s", "shots": [{"id": "s/1", "at": "start:a", "picture": {"background": "#111", "layers": []}}],
            "overlays": [{"id": "pop", "from": "word:a/two", "to": "end:a", "layers": [
                {"id": "pop/t", "type": "text", "text": "TWO", "size": 40}]}]}]
    });
    std::fs::write(d.join("timeline.json"), tl.to_string()).unwrap();
    d.join("timeline.json")
}

fn load(p: &Path) -> Project {
    Project::load(p).unwrap()
}

fn patch(base: u64, answers: &[&str], summary: &str, ops: Value) -> Value {
    json!({"base": base, "answers": answers, "summary": summary, "ops": ops})
}

#[test]
fn apply_log_revert() {
    let path = setup("alr");
    let p0 = load(&path);
    let a = apply(
        &p0,
        &patch(
            0,
            &["v1#3"],
            "Pop says THREE on 'three'",
            json!([
                {"op": "set", "id": "pop/t", "path": "text", "value": "THREE"},
                {"op": "set", "id": "pop", "path": "from", "value": "word:a/three"}
            ]),
        ),
    )
    .unwrap();
    commit(&a).unwrap();
    let p1 = load(&path);
    assert_eq!(p1.tl.rev, 1);
    assert_eq!(p1.doc["scenes"][0]["overlays"][0]["layers"][0]["text"], "THREE");
    let log = read_log(&p1.log_path()).unwrap();
    assert_eq!(log.len(), 1);
    assert_eq!(log[0].id, "p1");
    assert_eq!(log[0].answers, vec!["v1#3".to_string()]);
    assert_eq!(log[0].inverse.len(), 2);
    // Each log line validates against the schema.
    let line: Value =
        serde_json::from_str(std::fs::read_to_string(p1.log_path()).unwrap().lines().next().unwrap()).unwrap();
    assert_eq!(retake_timeline::schema::check_log_entry(&line), vec![]);

    // A second patch on another element.
    let b = apply(
        &p1,
        &patch(
            1,
            &[],
            "darker background",
            json!([
                {"op": "set", "id": "s/1", "path": "picture/background", "value": "#000"}
            ]),
        ),
    )
    .unwrap();
    commit(&b).unwrap();

    // Reverting p1 still works: p2 touched something else.
    let p2 = load(&path);
    let rv = revert_patch(&p2, "p1").unwrap();
    assert_eq!(rv["reverts"], "p1");
    let c = apply(&p2, &rv).unwrap();
    commit(&c).unwrap();
    let p3 = load(&path);
    assert_eq!(p3.tl.rev, 3);
    assert_eq!(p3.doc["scenes"][0]["overlays"][0]["layers"][0]["text"], "TWO");
    assert_eq!(p3.doc["scenes"][0]["overlays"][0]["from"], "word:a/two");
    assert_eq!(p3.doc["scenes"][0]["shots"][0]["picture"]["background"], "#000", "p2 survives");
    let log = read_log(&p3.log_path()).unwrap();
    assert_eq!(log.iter().map(|e| e.id.as_str()).collect::<Vec<_>>(), ["p1", "p2", "p3"]);
    assert_eq!(log[2].reverts.as_deref(), Some("p1"));
}

#[test]
fn revert_refuses_when_a_later_patch_touched_the_same_value() {
    let path = setup("conflict");
    let p0 = load(&path);
    commit(
        &apply(&p0, &patch(0, &[], "a", json!([{"op": "set", "id": "pop/t", "path": "text", "value": "A"}]))).unwrap(),
    )
    .unwrap();
    let p1 = load(&path);
    commit(
        &apply(&p1, &patch(1, &[], "b", json!([{"op": "set", "id": "pop/t", "path": "text", "value": "B"}]))).unwrap(),
    )
    .unwrap();
    let p2 = load(&path);
    let e = revert_patch(&p2, "p1").unwrap_err();
    assert_eq!(e.code, "patch_conflict");
    assert!(e.message.contains("p2"));
    // Reverting the later one first, then the earlier one, works.
    commit(&apply(&p2, &revert_patch(&p2, "p2").unwrap()).unwrap()).unwrap();
    let p3 = load(&path);
    commit(&apply(&p3, &revert_patch(&p3, "p1").unwrap()).unwrap()).unwrap();
    assert_eq!(load(&path).doc["scenes"][0]["overlays"][0]["layers"][0]["text"], "TWO");
    assert_eq!(revert_patch(&load(&path), "p9").unwrap_err().code, "patch_not_found");
}

#[test]
fn rejected_patches_change_nothing() {
    let path = setup("reject");
    let p0 = load(&path);
    let before = std::fs::read_to_string(&path).unwrap();
    // Wrong base.
    let e = apply(&p0, &patch(5, &[], "x", json!([{"op": "set", "id": "pop/t", "path": "text", "value": "X"}])))
        .err()
        .unwrap();
    assert_eq!(e.0[0].code, "patch_base_mismatch");
    // An anchor that doesn't resolve: the patch is refused with the resolver's error.
    let e = apply(&p0, &patch(0, &[], "x", json!([{"op": "set", "id": "pop", "path": "from", "value": "word:a/tow"}])))
        .err()
        .unwrap();
    assert_eq!(e.0[0].code, "anchor_word_not_found");
    assert_eq!(e.0[0].hint.as_deref(), Some("did you mean 'two'?"));
    // A schema violation.
    let e = apply(&p0, &patch(0, &[], "x", json!([{"op": "set", "id": "pop/t", "path": "size", "value": "big"}])))
        .err()
        .unwrap();
    assert_eq!(e.0[0].code, "schema");
    // A duplicate id.
    let e = apply(
        &p0,
        &patch(
            0,
            &[],
            "x",
            json!([{"op": "insert", "id": "pop", "path": "layers",
        "value": {"id": "pop/t", "type": "text", "text": "dup"}}]),
        ),
    )
    .err()
    .unwrap();
    assert_eq!(e.0[0].code, "duplicate_id");
    // A malformed patch.
    let e = apply(&p0, &json!({"base": 0, "ops": []})).err().unwrap();
    assert_eq!(e.0[0].code, "schema");
    assert_eq!(std::fs::read_to_string(&path).unwrap(), before);
    assert!(!p0.log_path().exists());
}

#[test]
fn insert_and_remove_elements_round_trip_through_revert() {
    let path = setup("insrem");
    let p0 = load(&path);
    commit(
        &apply(
            &p0,
            &patch(
                0,
                &["v1#1"],
                "add a second shot",
                json!([
                    {"op": "insert", "id": "s", "path": "shots", "after": "s/1",
                     "value": {"id": "s/2", "at": "word:a/three", "picture": {"background": "#222", "layers": []}}}
                ]),
            ),
        )
        .unwrap(),
    )
    .unwrap();
    let p1 = load(&path);
    assert_eq!(p1.tl.scenes[0].shots.len(), 2);
    commit(&apply(&p1, &revert_patch(&p1, "p1").unwrap()).unwrap()).unwrap();
    let p2 = load(&path);
    assert_eq!(p2.tl.scenes[0].shots.len(), 1);
    let mut want: Value = serde_json::from_str(&std::fs::read_to_string(&path).unwrap()).unwrap();
    want["rev"] = json!(0);
    assert_eq!(want, p0.doc, "back to the original document, apart from rev");
}
