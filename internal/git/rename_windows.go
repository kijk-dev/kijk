package git

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// rename tries again for a while: right after a clone, Defender or the indexer often holds a file
// in it open.
func rename(from, to string) error {
	end := time.Now().Add(2 * time.Second)
	for {
		err := os.Rename(from, to)
		busy := errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
		if !busy || time.Now().After(end) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}
