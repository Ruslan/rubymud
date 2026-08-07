package mapper

import "strings"

// ReplayLine is one canonical server-output line as it was seen by the live
// detector. Raw retains ANSI; Plain is the stripped form used by ordinary rules.
type ReplayLine struct {
	ID    int64
	Plain string
	Raw   string
}

type ReplaySample struct {
	LogID int64  `json:"log_id"`
	Hint  string `json:"hint"`
	Desc  string `json:"desc"`
	Exits string `json:"exits"`
}

type ReplayResult struct {
	ScannedLines   int            `json:"scanned_lines"`
	CandidateLines int            `json:"candidate_lines"`
	MatchedBlocks  int            `json:"matched_blocks"`
	Samples        []ReplaySample `json:"samples"`
}

// ReplayProfile runs an isolated detector over chronological saved output. It
// never touches a live session or tracker. sampleLimit retains the newest
// matches while the aggregate counters cover the entire input.
func ReplayProfile(profile Profile, lines []ReplayLine, sampleLimit int) ReplayResult {
	result := ReplayResult{ScannedLines: len(lines), Samples: make([]ReplaySample, 0, sampleLimit)}
	detector := NewSpecDetector(profile, DetectorInfo{})
	for _, line := range lines {
		if profile.LooksLikeExits(strings.TrimSpace(strings.TrimRight(line.Plain, "\r"))) {
			result.CandidateLines++
		}
		raw := line.Raw
		if raw == "" {
			raw = line.Plain
		}
		event, ok := detector.Feed(Line{Stripped: line.Plain, Raw: raw})
		if !ok {
			continue
		}
		result.MatchedBlocks++
		if sampleLimit <= 0 {
			continue
		}
		sample := ReplaySample{LogID: line.ID, Hint: event.Hint, Desc: event.Desc, Exits: event.Exits}
		if len(result.Samples) == sampleLimit {
			copy(result.Samples, result.Samples[1:])
			result.Samples[len(result.Samples)-1] = sample
		} else {
			result.Samples = append(result.Samples, sample)
		}
	}
	return result
}
