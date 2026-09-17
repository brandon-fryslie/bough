package perf

import (
	"math"
	"slices"
	"time"
)

// Summary is how a recording drew, in numbers that compare across runs and
// browsers.
type Summary struct {
	// Refresh is the browser's frame interval at rest, learned from the quiet
	// frames before input. Everything else is judged against it, so a 120 Hz
	// display and a 60 Hz one are held to what each can actually do.
	Refresh time.Duration

	// Frames is how many frame intervals overlapped the input.
	Frames int

	// Median, P95 and Worst describe those intervals. At rest each would be
	// Refresh.
	Median time.Duration
	P95    time.Duration
	Worst  time.Duration

	// Missed is how many frames the browser would have started at its resting
	// rate but did not, because it was busy.
	Missed int
}

// Summarize judges a recording.
//
// The frames that count are those whose interval overlapped the input: from
// the interval that ended at or after the first input to the one that began at
// or before the last. A ParseRecording guarantees at least one, since a frame
// always follows the last input.
func Summarize(r Recording) Summary {
	first, last := r.inputs[0], r.inputs[len(r.inputs)-1]

	var quiet, busy []time.Duration
	for i := 1; i < len(r.frames); i++ {
		start, end := r.frames[i-1], r.frames[i]
		gap := end - start
		switch {
		case end < first:
			quiet = append(quiet, gap)
		case start <= last:
			busy = append(busy, gap)
		}
	}

	refresh := percentile(quiet, 0.5)
	missed := 0
	for _, gap := range busy {
		missed += max(int(math.Round(float64(gap)/float64(refresh)))-1, 0)
	}

	return Summary{
		Refresh: refresh,
		Frames:  len(busy),
		Median:  percentile(busy, 0.5),
		P95:     percentile(busy, 0.95),
		Worst:   slices.Max(busy),
		Missed:  missed,
	}
}

// percentile is the nearest-rank percentile: the smallest value at least a
// share p of the values are no greater than.
func percentile(values []time.Duration, p float64) time.Duration {
	sorted := slices.Sorted(slices.Values(values))
	rank := int(math.Ceil(p * float64(len(sorted))))
	return sorted[max(rank, 1)-1]
}
