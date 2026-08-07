//go:build !race

package conformance

// raceDetectorEnabled reports whether this binary was built with -race.
//
// It exists for ONE check: the zero-allocation guarantee. The race detector
// instruments allocation accounting and makes regexp's pooled-machine path
// allocate where it does not in a normal build — measured: `diku-en`'s filler
// (a line that passes the cheap literal guard and then fails the exits regex)
// reports 0 allocs/op normally and 1 under -race. That is an artifact of the
// instrumentation, not of the detector.
//
// So the allocation assertion runs in the ordinary `go test ./...` pass and is
// skipped under -race. It is NOT lost: the ordinary pass is part of this
// project's definition of done, and the skip says so out loud rather than
// quietly reporting success.
const raceDetectorEnabled = false
