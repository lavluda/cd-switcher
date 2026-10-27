package usage

import (
	"encoding/json"
	"errors"
	"os"
	"time"
)

// dailyKeepDays bounds how many days of daily peaks are stored.
const dailyKeepDays = 30

// Day is one calendar day's usage peaks and how many session limits filled.
type Day struct {
	PeakSession float64 `json:"peakSession"`
	PeakWeek    float64 `json:"peakWeek"`
	Hits        int     `json:"hits"`
}

// Daily is a profile's per-day peaks, kept far longer than the 24h Sample
// history so a weekly summary has something to read. LastHit is the reset time
// of the last session window counted as a hit, so one full window counts once.
type Daily struct {
	Days    map[string]Day `json:"days"`
	LastHit int64          `json:"lastHit,omitempty"`
}

const dayFormat = "2006-01-02"

// LoadDaily reads a profile's daily file, returning an empty Daily if it
// doesn't exist yet.
func LoadDaily(path string) (Daily, error) {
	var d Daily
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	err = json.Unmarshal(data, &d)
	return d, err
}

// RecordDaily folds one reading into today's peaks and rewrites the file
// atomically. resetUnix is the session window's reset time.
func RecordDaily(path string, now time.Time, session, week float64, resetUnix int64) error {
	d, err := LoadDaily(path)
	if err != nil {
		d = Daily{} // corrupt file: start fresh, as AppendHistory does
	}
	if d.Days == nil {
		d.Days = map[string]Day{}
	}
	key := now.Local().Format(dayFormat)
	day := d.Days[key]
	day.PeakSession = max(day.PeakSession, session)
	day.PeakWeek = max(day.PeakWeek, week)
	if session >= 100 && d.LastHit != resetUnix {
		day.Hits++
		d.LastHit = resetUnix
	}
	d.Days[key] = day
	cutoff := now.Local().AddDate(0, 0, -dailyKeepDays).Format(dayFormat)
	for k := range d.Days {
		if k < cutoff {
			delete(d.Days, k)
		}
	}

	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Summary combines the last n days up to and including now: highest peaks and
// total hits. Days is how many of them had any reading.
func (d Daily) Summary(now time.Time, n int) (total Day, days int) {
	for i := 0; i < n; i++ {
		day, ok := d.Days[now.Local().AddDate(0, 0, -i).Format(dayFormat)]
		if !ok {
			continue
		}
		days++
		total.PeakSession = max(total.PeakSession, day.PeakSession)
		total.PeakWeek = max(total.PeakWeek, day.PeakWeek)
		total.Hits += day.Hits
	}
	return total, days
}
