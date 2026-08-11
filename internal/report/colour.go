package report

import (
	"fmt"
	"time"
)

// Score bands used to colour the score and its bar. A high mutation score is
// unusual and worth celebrating; a low one is the point of running the tool.
const (
	scoreGood = 80.0
	scoreOkay = 50.0
)

// colours renders text with optional ANSI styling. Every method is identity when
// colour is off, so callers need no conditionals.
type colours struct{ on bool }

func palette(on bool) colours { return colours{on: on} }

func (c colours) wrap(code, s string) string {
	if !c.on {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (c colours) bold(s string) string    { return c.wrap("1", s) }
func (c colours) faint(s string) string   { return c.wrap("2", s) }
func (c colours) added(s string) string   { return c.wrap("32", s) }
func (c colours) removed(s string) string { return c.wrap("31", s) }
func (c colours) warn(s string) string    { return c.wrap("33", s) }
func (c colours) pass(s string) string    { return c.wrap("32", s) }
func (c colours) fail(s string) string    { return c.wrap("31", s) }

// forScore colours text by score band: green when strong, yellow when middling,
// red when the tests are letting mutations through.
func (c colours) forScore(score float64, s string) string {
	switch {
	case score >= scoreGood:
		return c.wrap("32", s)
	case score >= scoreOkay:
		return c.wrap("33", s)
	default:
		return c.wrap("31", s)
	}
}

// short formats a duration for humans: milliseconds for fast runs, seconds
// otherwise, never the full Go form with nanosecond noise.
func short(d time.Duration) string {
	switch {
	case d == 0:
		return "0s"
	case d < time.Millisecond:
		return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000)
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}
