pub fn probe() -> usize {
    let s = skia_safe::surfaces::raster_n32_premul((4, 4)).unwrap();
    let _ = rquickjs::Runtime::new().unwrap();
    let _sh = skia_safe::Shaper::new(None);
    s.width() as usize
}
