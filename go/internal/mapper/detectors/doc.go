// Package detectors is the home for CONTRIBUTED Go room detectors.
//
// rubymud is open-source and its mapper has to work on MUDs we have never seen.
// Most formats are describable with the declarative regex spec (a "room format"
// in the settings UI) and need no code at all — that path is the default and the
// one a non-programmer uses. This package is for the rest: a format whose room
// name is recognizable only by its colour, whose block boundary is stateful,
// whose exits line needs arithmetic. Those cannot be a regex, and the honest
// answer is a small Go file that we review and compile in.
//
// # How to contribute a detector
//
//  1. Add ONE file to this package: `mymud.go`.
//  2. Define a type implementing mapper.RoomDetector (Feed/Reset/Describe) and
//     mapper.Conformer (ConformanceCases/FillerLine).
//  3. Register it from init():  func init() { mapper.RegisterDetector(newMyMUD) }
//  4. Run `go test ./go/internal/mapper/detectors/...`. Nothing else is needed:
//     conformance_test.go already runs the shared suite over every registered
//     detector, and imports_test.go already checks your imports.
//
// example.go is a complete, working, deliberately tiny example of all four.
//
// # The rules, and why each one is enforced by a test rather than a review note
//
//   - A detector takes lines and returns events. NOTHING ELSE. No network, no
//     disk, no subprocesses, no database, no goroutines, no global mutable state
//     beyond the registration itself. imports_test.go fails the build on a
//     denylisted import and on any rubymud package other than `mapper`, because
//     "please don't" in a CONTRIBUTING file does not survive a rebase.
//
//   - Feed runs on EVERY incoming line, in the session's read loop, on the
//     latency-tracked path (AGENTS.md #3). On a line that does not complete a
//     block it must not allocate: guard with a cheap literal prefix compare
//     before any regex, and never build a string. conformance_test.go measures
//     it.
//
//   - A panic cannot take the client down — the session wraps Feed, and
//     mapper.SafeNewDetector/SafeSuppressUntilBoundary wrap your factory, your
//     Describe() and the optional BoundaryAware calls. A panic in any of them logs,
//     falls that ONE session back to the default, and reports it in the settings
//     UI. That is a safety net for a bug we did not catch, not a licence: a
//     detector that panics is a broken detector and will be reverted.
//
//   - THE ONE UNGUARDED MOMENT IS init(). A panic there happens before main() and
//     cannot be recovered by anything — the Go runtime tears the process down, and
//     the host will not boot at all. So keep init() to the single
//     mapper.RegisterDetector call. Do not compile regexes, read files, or
//     validate anything in it. (This is also the least dangerous place to get it
//     wrong: it fails identically on every machine the instant the binary starts,
//     including your own `go test`, rather than on someone else's MUD at 2am.)
//     Note that RegisterDetector itself panics by design on a duplicate key or a
//     bogus provenance — that is a build-time programming error, caught on the
//     first run of any binary that links you.
//
//   - No goroutines, no `select`, no `runtime`. Enforced by imports_test.go. Your
//     detector is called synchronously from one session's read loop and needs no
//     concurrency; a goroutine you spawn escapes the panic boundary entirely
//     (recover only covers the goroutine that panics), and runtime.Goexit() would
//     kill the read loop while recover() reports nothing at all.
//
//   - `time` is not banned, but treat it as discouraged. There is no static rule
//     that can protect the read loop here — a plain `for {}` blocks it just as
//     dead as a sleep, and banning one while allowing the other is theatre. The
//     real contract is the one at the top: Feed is cheap and total. Sleeping,
//     polling or waiting in it stalls every line that session receives.
//
//   - Your fixtures ARE your correctness claim. "Contributed" provenance means
//     exactly "does what its author says on the blocks its author supplied". Ship
//     fixtures that cover the shapes your MUD really prints, including at least
//     one run that must emit NOTHING.
//
// # Linking
//
// Registration is compile-time, database/sql-driver style, so something must
// import this package for its init() to run. cmd/mudhost does that with a blank
// import. A detector in a package nobody imports is a detector nobody has.
package detectors
