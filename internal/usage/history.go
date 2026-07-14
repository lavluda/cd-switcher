package usage

import (
	"encoding/json"
	"errors"
	"os"
	"time"
)

// Sample is one polled usage-utilization data point for a profile.
type Sample struct {
	Time     time.Time `json:"time"`
	FiveHour float64   `json:"fiveHour"` // utilization %, 0-100
	SevenDay float64   `json:"sevenDay"` // utilization %, 0-100
}

// MaxHistorySamples caps stored history at roughly 24h of 5-minute samples.
const MaxHistorySamples = 288

// LoadHistory reads a profile's usage history file, returning (nil, nil) if
// it doesn't exist yet.
func LoadHistory(path string) ([]Sample, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var samples []Sample
	if err := json.Unmarshal(data, &samples); err != nil {
		return nil, err
	}
	return samples, nil
}

// AppendHistory adds one sample and atomically rewrites the history file
// (temp file + rename, matching profile.Store.Save's pattern), trimming to
// the newest MaxHistorySamples entries.
func AppendHistory(path string, s Sample) error {
	samples, err := LoadHistory(path)
	if err != nil {
		// A corrupt/unreadable history file shouldn't block new samples from
		// being recorded — start fresh rather than failing forever.
		samples = nil
	}
	samples = append(samples, s)
	if len(samples) > MaxHistorySamples {
		samples = samples[len(samples)-MaxHistorySamples:]
	}

	data, err := json.Marshal(samples)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
