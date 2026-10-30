//! Retake editor renderer: ffmpeg pipes for decode and encode, a Rust compositor, Skia graphics, QuickJS expressions,
//! a content-addressed cache with two keys (layer and unit), a one-pass audio mix, and the render manifest.

pub mod audio;
pub mod composite;
pub mod engine;
pub mod expr;
pub mod ffmpeg;
pub mod graphics;
pub mod lint;
pub mod pipeline;
pub mod preview;
pub mod proxy;
pub mod store;
pub mod transform;
pub mod unit;

pub use engine::{Engine, Options};
pub use pipeline::{PlanReport, RenderReport, default_output, plan, render, stills};

/// Sets the renderer up for the timeline crate: QuickJS for component `$expr` templates and custom lint rules. Call it
/// before loading a timeline (`Engine::open` does).
pub fn init() {
    expr::register();
}
