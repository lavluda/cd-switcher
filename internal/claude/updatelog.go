package claude

import (
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

// updateQuitMarker is what Claude Desktop writes to its main log right before
// it quits to install an update. A plain quit never writes it, so it tells an
// update restart apart from a window the user closed on purpose.
const updateQuitMarker = "beforeQuitForUpdate handler fired"

// logTailBytes bounds how much of the log is read; the marker is only ever
// interesting when it is recent.
const logTailBytes = 512 << 10

const logTimeLayout = "2006-01-02 15:04:05"

// LastUpdateQuit returns when Claude Desktop last logged that it was quitting
// to install an update. The log is shared by every Claude process, so this does
// not say which window quit — only that an update restart happened.
func LastUpdateQuit() (time.Time, bool) {
	path, err := mainLogPath()
	if err != nil {
		return time.Time{}, false
	}
	tail, err := readTail(path, logTailBytes)
	if err != nil {
		return time.Time{}, false
	}
	return parseLastUpdateQuit(tail, time.Local)
}

// parseLastUpdateQuit finds the newest update-quit line in log text whose lines
// start with a "2006-01-02 15:04:05" local timestamp.
func parseLastUpdateQuit(text string, loc *time.Location) (time.Time, bool) {
	var last time.Time
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, updateQuitMarker) || len(line) < len(logTimeLayout) {
			continue
		}
		t, err := time.ParseInLocation(logTimeLayout, line[:len(logTimeLayout)], loc)
		if err != nil {
			continue
		}
		if t.After(last) {
			last = t
		}
	}
	return last, !last.IsZero()
}

func readTail(path string, n int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() == 0 {
		return "", errors.New("empty log")
	}
	if info.Size() > n {
		if _, err := f.Seek(-n, io.SeekEnd); err != nil {
			return "", err
		}
	}
	b, err := io.ReadAll(f)
	return string(b), err
}
