//go:build unix

package updater

import (
	"os"
	"syscall"
)

func getFileStatImpl(info os.FileInfo) (fileStat, bool) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return fileStat{uid: stat.Uid, gid: stat.Gid}, true
	}
	return fileStat{}, false
}
