//! Fragmented MP4 for HLS (spec/bridge.md, "Streaming"): just enough ISO BMFF to split ffmpeg's fragmented output of a
//! unit into its init segment and its fragments, and to put the fragments on the stream's clock in place.
//!
//! ffmpeg starts every track of every file at decode time 0, and with negative composition offsets it still gives
//! the first video frame an offset of one frame (the B-frame delay it would otherwise put in an edit list). Players
//! key on decode times: hls.js maps the first fragment's lowest `tfdt` to the start of the playlist. So each fragment's
//! `tfdt` is set to the time it plays in the whole render, and the video's composition offsets are shifted so the
//! first frame's is 0 (its decode time is its presentation time). Then a frame shows at exactly its output time, the
//! same moment as in the MP4 export, and the audio stays in sync with it. Sizes never change, so the patches are in
//! place.

use anyhow::{Context, Result, bail};
use std::collections::BTreeMap;
use std::ops::Range;

/// A box: its type, and where its whole and its body are in the file.
#[derive(Debug, Clone)]
struct Bx {
    kind: [u8; 4],
    all: Range<usize>,
    body: Range<usize>,
}

fn boxes(b: &[u8], within: Range<usize>) -> Result<Vec<Bx>> {
    let mut out = vec![];
    let mut at = within.start;
    while at + 8 <= within.end {
        let size32 = u32::from_be_bytes(b[at..at + 4].try_into()?) as usize;
        let kind: [u8; 4] = b[at + 4..at + 8].try_into()?;
        let (size, head) = match size32 {
            0 => (within.end - at, 8),
            1 => (u64::from_be_bytes(b.get(at + 8..at + 16).context("a cut-off box")?.try_into()?) as usize, 16),
            n => (n, 8),
        };
        if size < head || at + size > within.end {
            bail!("a bad {} box at byte {at}", String::from_utf8_lossy(&kind));
        }
        out.push(Bx { kind, all: at..at + size, body: at + head..at + size });
        at += size;
    }
    Ok(out)
}

fn child<'a>(bs: &'a [Bx], kind: &[u8; 4]) -> Option<&'a Bx> {
    bs.iter().find(|x| &x.kind == kind)
}

fn u32_at(b: &[u8], at: usize) -> Result<u32> {
    Ok(u32::from_be_bytes(b.get(at..at + 4).context("a cut-off box")?.try_into()?))
}

fn u64_at(b: &[u8], at: usize) -> Result<u64> {
    Ok(u64::from_be_bytes(b.get(at..at + 8).context("a cut-off box")?.try_into()?))
}

/// A track of the init segment.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Track {
    pub video: bool,
    pub timescale: u32,
}

/// Where one track's numbers are in a fragment.
#[derive(Debug, Clone)]
struct Traf {
    track: u32,
    /// The `tfdt` value's byte offset and width (4 or 8).
    tfdt: (usize, usize),
    /// Byte offsets of each sample's composition offset, in the `trun`s (empty when they have none).
    ctos: Vec<usize>,
    /// The composition offsets are signed (`trun` version 1).
    signed: bool,
}

/// A fragment: its `moof` and `mdat` bytes, and its tracks.
#[derive(Debug, Clone)]
pub struct Fragment {
    pub bytes: Range<usize>,
    trafs: Vec<Traf>,
}

/// A fragmented MP4 split up: the init segment (`ftyp` and `moov`), its tracks by id, and the fragments in order.
#[derive(Debug)]
pub struct Fragmented {
    pub data: Vec<u8>,
    pub init: Range<usize>,
    pub tracks: BTreeMap<u32, Track>,
    pub fragments: Vec<Fragment>,
}

impl Fragmented {
    pub fn parse(data: Vec<u8>) -> Result<Fragmented> {
        let top = boxes(&data, 0..data.len())?;
        let moov = child(&top, b"moov").context("no moov box")?;
        let init = 0..moov.all.end;
        if top.iter().take_while(|x| x.kind != *b"moov").any(|x| x.kind != *b"ftyp") {
            bail!("the init segment isn't just ftyp and moov");
        }
        let mut tracks = BTreeMap::new();
        for trak in boxes(&data, moov.body.clone())?.iter().filter(|x| x.kind == *b"trak") {
            let tb = boxes(&data, trak.body.clone())?;
            let tkhd = child(&tb, b"tkhd").context("no tkhd")?;
            let id = u32_at(&data, tkhd.body.start + if data[tkhd.body.start] == 1 { 20 } else { 12 })?;
            let mdia = boxes(&data, child(&tb, b"mdia").context("no mdia")?.body.clone())?;
            let mdhd = child(&mdia, b"mdhd").context("no mdhd")?;
            let timescale = u32_at(&data, mdhd.body.start + if data[mdhd.body.start] == 1 { 20 } else { 12 })?;
            let hdlr = child(&mdia, b"hdlr").context("no hdlr")?;
            let video = data.get(hdlr.body.start + 8..hdlr.body.start + 12) == Some(b"vide");
            tracks.insert(id, Track { video, timescale });
        }
        let mut fragments: Vec<Fragment> = vec![];
        let mut open: Option<Fragment> = None;
        for b in top.iter().skip_while(|x| x.kind != *b"moov").skip(1) {
            match &b.kind {
                b"moof" => {
                    if let Some(f) = open.take() {
                        fragments.push(f);
                    }
                    let mut trafs = vec![];
                    for traf in boxes(&data, b.body.clone())?.iter().filter(|x| x.kind == *b"traf") {
                        trafs.push(read_traf(&data, traf)?);
                    }
                    open = Some(Fragment { bytes: b.all.clone(), trafs });
                }
                b"mdat" => {
                    let f = open.as_mut().context("an mdat before any moof")?;
                    f.bytes.end = b.all.end;
                }
                _ => {} // sidx, mfra, free: not needed for HLS
            }
        }
        fragments.extend(open);
        Ok(Fragmented { data, init, tracks, fragments })
    }

    fn video(&self) -> Option<(u32, Track)> {
        self.tracks.iter().find(|(_, t)| t.video).map(|(id, t)| (*id, *t))
    }

    /// The decode time of a fragment's video (its first frame, at a keyframe), in the video's timescale.
    pub fn video_start(&self, f: &Fragment) -> Option<u64> {
        let (id, _) = self.video()?;
        let t = f.trafs.iter().find(|t| t.track == id)?;
        read_tfdt(&self.data, t.tfdt).ok()
    }

    /// Puts every fragment on the stream's clock: each track's decode times move by `shift(track)` (in its own
    /// timescale), and the video's composition offsets so that its first frame's is 0.
    pub fn retime(&mut self, shift: impl Fn(Track) -> u64) -> Result<()> {
        let first = self.video().and_then(|(id, _)| {
            let t = self.fragments.first()?.trafs.iter().find(|t| t.track == id)?;
            Some(i32::from_be_bytes(self.data[*t.ctos.first()?..][..4].try_into().ok()?))
        });
        for f in &self.fragments {
            for t in &f.trafs {
                let tr = *self.tracks.get(&t.track).context("a fragment of an unknown track")?;
                let v = read_tfdt(&self.data, t.tfdt)? + shift(tr);
                match t.tfdt.1 {
                    8 => self.data[t.tfdt.0..t.tfdt.0 + 8].copy_from_slice(&v.to_be_bytes()),
                    _ => self.data[t.tfdt.0..t.tfdt.0 + 4]
                        .copy_from_slice(&u32::try_from(v).context("a decode time past 32 bits")?.to_be_bytes()),
                }
                if let (true, Some(c0)) = (tr.video, first)
                    && c0 != 0
                {
                    if !t.signed {
                        bail!("the video's composition offsets can't be shifted (trun version 0)");
                    }
                    for &at in &t.ctos {
                        let c = i32::from_be_bytes(self.data[at..at + 4].try_into()?) - c0;
                        self.data[at..at + 4].copy_from_slice(&c.to_be_bytes());
                    }
                }
            }
        }
        Ok(())
    }
}

impl Fragmented {
    /// Zeroes the bitrates the init segment states (`btrt`, and the `esds` decoder config), which ffmpeg measures on
    /// each file: 0 means "unknown", and then units encoded alike have byte-identical init segments, so a playlist
    /// needs one `EXT-X-MAP` instead of one per unit.
    pub fn clear_bitrates(&mut self) -> Result<()> {
        let mut zero: Vec<Range<usize>> = vec![];
        let d = &self.data;
        let mut stack = boxes(d, self.init.clone())?;
        while let Some(b) = stack.pop() {
            match &b.kind {
                b"moov" | b"trak" | b"mdia" | b"minf" | b"stbl" => stack.extend(boxes(d, b.body.clone())?),
                b"stsd" => stack.extend(boxes(d, b.body.start + 8..b.body.end)?),
                // Sample entries: their own fields, then child boxes.
                b"avc1" | b"avc3" | b"hvc1" | b"hev1" if b.body.len() >= 78 => {
                    stack.extend(boxes(d, b.body.start + 78..b.body.end)?)
                }
                b"mp4a" if b.body.len() >= 28 => stack.extend(boxes(d, b.body.start + 28..b.body.end)?),
                b"btrt" => zero.push(b.body.clone()),
                b"esds" => {
                    if let Some(r) = decoder_config_rates(d, b.body.start + 4, b.body.end) {
                        zero.push(r);
                    }
                }
                _ => {}
            }
        }
        for r in zero {
            self.data[r].fill(0);
        }
        Ok(())
    }
}

/// An MPEG-4 descriptor's length (1–4 bytes, 7 bits each) at `at`: the length and where the content starts.
fn descriptor_len(b: &[u8], mut at: usize) -> Option<(usize, usize)> {
    let mut len = 0usize;
    for _ in 0..4 {
        let x = *b.get(at)?;
        at += 1;
        len = (len << 7) | (x & 0x7F) as usize;
        if x & 0x80 == 0 {
            return Some((len, at));
        }
    }
    None
}

/// The buffer size and bitrates of an `esds`'s DecoderConfigDescriptor, from its ES_Descriptor at `at`.
fn decoder_config_rates(b: &[u8], at: usize, end: usize) -> Option<Range<usize>> {
    if *b.get(at)? != 0x03 {
        return None;
    }
    let (_, mut p) = descriptor_len(b, at + 1)?;
    let flags = *b.get(p + 2)?;
    p += 3;
    if flags & 0x80 != 0 {
        p += 2;
    }
    if flags & 0x40 != 0 {
        p += 1 + *b.get(p)? as usize;
    }
    if flags & 0x20 != 0 {
        p += 2;
    }
    if *b.get(p)? != 0x04 {
        return None;
    }
    let (_, c) = descriptor_len(b, p + 1)?;
    // objectTypeIndication, streamType, then bufferSizeDB (3), maxBitrate (4), avgBitrate (4).
    (c + 13 <= end).then_some(c + 2..c + 13)
}

fn read_tfdt(b: &[u8], (at, width): (usize, usize)) -> Result<u64> {
    if width == 8 { u64_at(b, at) } else { u32_at(b, at).map(u64::from) }
}

fn read_traf(b: &[u8], traf: &Bx) -> Result<Traf> {
    let kids = boxes(b, traf.body.clone())?;
    let tfhd = child(&kids, b"tfhd").context("no tfhd")?;
    let track = u32_at(b, tfhd.body.start + 4)?;
    let tfdt = child(&kids, b"tfdt").context("no tfdt")?;
    let tfdt = (tfdt.body.start + 4, if b[tfdt.body.start] == 1 { 8 } else { 4 });
    let mut ctos = vec![];
    let mut signed = true;
    for trun in kids.iter().filter(|x| x.kind == *b"trun") {
        let s = trun.body.start;
        let (version, flags) = (b[s], u32_at(b, s)? & 0xFF_FFFF);
        let count = u32_at(b, s + 4)? as usize;
        let mut at = s + 8;
        if flags & 0x1 != 0 {
            at += 4; // data offset
        }
        if flags & 0x4 != 0 {
            at += 4; // first sample flags
        }
        let per = [0x100, 0x200, 0x400, 0x800].iter().filter(|f| flags & **f != 0).count() * 4;
        if flags & 0x800 != 0 {
            signed &= version == 1;
            let skip = [0x100, 0x200, 0x400].iter().filter(|f| flags & **f != 0).count() * 4;
            for i in 0..count {
                ctos.push(at + i * per + skip);
            }
        }
        if at + count * per > trun.body.end {
            bail!("a cut-off trun");
        }
    }
    Ok(Traf { track, tfdt, ctos, signed })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn bx(kind: &[u8; 4], body: &[u8]) -> Vec<u8> {
        let mut v = ((body.len() + 8) as u32).to_be_bytes().to_vec();
        v.extend_from_slice(kind);
        v.extend_from_slice(body);
        v
    }

    fn full(version: u8, flags: u32, rest: &[u8]) -> Vec<u8> {
        let mut v = vec![version];
        v.extend_from_slice(&flags.to_be_bytes()[1..]);
        v.extend_from_slice(rest);
        v
    }

    fn trak(id: u32, handler: &[u8; 4], timescale: u32) -> Vec<u8> {
        let tkhd = bx(b"tkhd", &full(0, 3, &[[0u8; 8].as_slice(), &id.to_be_bytes(), &[0u8; 68]].concat()));
        let mdhd = bx(b"mdhd", &full(0, 0, &[[0u8; 8].as_slice(), &timescale.to_be_bytes(), &[0u8; 8]].concat()));
        let hdlr = bx(b"hdlr", &full(0, 0, &[[0u8; 4].as_slice(), handler, &[0u8; 13]].concat()));
        bx(b"trak", &[tkhd, bx(b"mdia", &[mdhd, hdlr].concat())].concat())
    }

    /// A fragment: video with three samples (durations and composition offsets), audio with two.
    fn fragment(vt: u64, ctos: [i32; 3], at: u64) -> Vec<u8> {
        let vtfhd = bx(b"tfhd", &full(0, 0x020000, &1u32.to_be_bytes()));
        let vtfdt = bx(b"tfdt", &full(1, 0, &vt.to_be_bytes()));
        let mut samples = vec![];
        for c in ctos {
            samples.extend_from_slice(&512u32.to_be_bytes());
            samples.extend_from_slice(&c.to_be_bytes());
        }
        let vtrun =
            bx(b"trun", &full(1, 0x1 | 0x100 | 0x800, &[3u32.to_be_bytes().as_slice(), &[0; 4], &samples].concat()));
        let atfhd = bx(b"tfhd", &full(0, 0x020000, &2u32.to_be_bytes()));
        let atfdt = bx(b"tfdt", &full(1, 0, &at.to_be_bytes()));
        let atrun = bx(b"trun", &full(0, 0x200, &[2u32.to_be_bytes().as_slice(), &[0; 8]].concat()));
        let moof = bx(
            b"moof",
            &[bx(b"traf", &[vtfhd, vtfdt, vtrun].concat()), bx(b"traf", &[atfhd, atfdt, atrun].concat())].concat(),
        );
        [moof, bx(b"mdat", b"media")].concat()
    }

    #[test]
    fn fragments_go_on_the_stream_clock_with_the_first_frame_at_its_decode_time() {
        let file = [
            bx(b"ftyp", b"isom"),
            bx(b"moov", &[trak(1, b"vide", 15360), trak(2, b"soun", 48000)].concat()),
            fragment(0, [512, 1536, 0], 0),
            fragment(1536, [512, 1536, 0], 3072),
            bx(b"mfra", b""),
        ]
        .concat();
        let mut f = Fragmented::parse(file.clone()).unwrap();
        assert_eq!(f.tracks[&1], Track { video: true, timescale: 15360 });
        assert_eq!(f.tracks[&2], Track { video: false, timescale: 48000 });
        assert_eq!(f.fragments.len(), 2);
        assert_eq!(&f.data[f.init.clone()], &file[..f.init.end]);
        assert_eq!(f.video_start(&f.fragments[1]), Some(1536));
        f.retime(|t| if t.video { 15360 * 3 } else { 48000 * 3 + 900 }).unwrap();
        let again = Fragmented::parse(f.data.clone()).unwrap();
        assert_eq!(again.video_start(&again.fragments[0]), Some(46080));
        assert_eq!(again.video_start(&again.fragments[1]), Some(46080 + 1536));
        let cto = |fr: &Fragment, i: usize| {
            let at = fr.trafs[0].ctos[i];
            i32::from_be_bytes(again.data[at..at + 4].try_into().unwrap())
        };
        assert_eq!(
            [cto(&again.fragments[0], 0), cto(&again.fragments[0], 1), cto(&again.fragments[0], 2)],
            [0, 1024, -512]
        );
        assert_eq!(cto(&again.fragments[1], 0), 0, "every fragment shifts alike");
        let atfdt = again.fragments[1].trafs[1].tfdt;
        assert_eq!(read_tfdt(&again.data, atfdt).unwrap(), 3072 + 48000 * 3 + 900);
        assert_eq!(again.fragments[1].bytes.end, file.len() - 8, "the mfra isn't part of a fragment");
        assert!(Fragmented::parse(b"\0\0\0\x08moov\0\0".to_vec()).is_ok());
        assert!(Fragmented::parse(b"\0\0\0\x20moov".to_vec()).is_err());
    }
}
