package worker

import (
	"os"
	"syscall"
)

// ficlone is Linux's FICLONE ioctl: dest shares src's blocks, copy on write (btrfs, XFS, bcachefs).
const ficlone = 0x40049409

// reflink makes out a copy-on-write clone of in, where the file system can; false when it can't.
func reflink(out, in *os.File) bool {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, out.Fd(), ficlone, in.Fd())
	return errno == 0
}
