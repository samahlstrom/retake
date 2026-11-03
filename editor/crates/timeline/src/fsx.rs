//! File helpers that behave the same on every system.

use std::io;
use std::path::Path;

/// How many times [`replace`] tries again on Windows, and how long it waits between tries (about two seconds in all).
const TRIES: u32 = 80;
const PAUSE: std::time::Duration = std::time::Duration::from_millis(25);

/// Renames `tmp` over `path`, replacing it, so a reader sees the old file or the new one, never half. On Windows a file
/// another program has open without delete sharing can't be replaced while it's open, and Retake's server (Go, which
/// opens files that way) reads the timeline and the playlists while they're rewritten: the rename is tried again for
/// a moment then. Elsewhere it's one rename.
pub fn replace(tmp: &Path, path: &Path) -> io::Result<()> {
    let mut tries = 0;
    loop {
        match std::fs::rename(tmp, path) {
            Err(e) if cfg!(windows) && tries < TRIES && busy(&e) => {
                tries += 1;
                std::thread::sleep(PAUSE);
            }
            r => return r,
        }
    }
}

/// ERROR_ACCESS_DENIED (5) and ERROR_SHARING_VIOLATION (32): on Windows, the file is open elsewhere for now.
fn busy(e: &io::Error) -> bool {
    matches!(e.raw_os_error(), Some(5 | 32))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn replace_puts_the_new_file_in_place() {
        let dir = std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../target/tmp/fsx");
        std::fs::create_dir_all(&dir).unwrap();
        let (tmp, path) = (dir.join("a.json.tmp"), dir.join("a.json"));
        std::fs::write(&path, "old").unwrap();
        std::fs::write(&tmp, "new").unwrap();
        replace(&tmp, &path).unwrap();
        assert_eq!(std::fs::read_to_string(&path).unwrap(), "new");
        assert!(!tmp.exists());
        assert!(replace(&tmp, &path).is_err(), "a missing file isn't retried for ever");
        let _ = std::fs::remove_dir_all(&dir);
    }
}
