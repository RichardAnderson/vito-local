//go:build !unix

package updater

import "os"

func getFileStatImpl(info os.FileInfo) (fileStat, bool) {
	return fileStat{}, false
}
