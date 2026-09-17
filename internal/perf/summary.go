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
// A frame's time is when it starts, so the interval beginning at a frame is
// what that frame cost. The intervals that count run from the one spanning the
// first input to the one beginning at the first frame at or after the last
// input, which is the frame that handled it. The quiet intervals are those
// ended before any input. ParseRecording guarantees both are there.
func Summarize(r Recording) Summary {
	first := countBefore(r.frames, r.inputs[0])
	handled := countBefore(r.frames, r.inputs[len(r.inputs)-1])

	// [LAW:dataflow-not-control-flow] which intervals count is where the
	// slices are cut, not a decision made per interval.
	quiet := gaps(r.frames[:first])
	busy := gaps(r.frames[first-1 : handled+2])

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

// gaps is the interval between each frame and the next.
func gaps(frames []time.Duration) []time.Duration {
	out := make([]time.Duration, 0, max(len(frames)-1, 0))
	for i := 1; i < len(frames); i++ {
		out = append(out, frames[i]-frames[i-1])
	}
	return out
}

// percentile is the nearest-rank percentile: the smallest value at least a
// share p of the values are no greater than.
func percentile(values []time.Duration, p float64) time.Duration {
	sorted := slices.Sorted(slices.Values(values))
	rank := int(math.Ceil(p * float64(len(sorted))))
	return sorted[max(rank, 1)-1]
}
