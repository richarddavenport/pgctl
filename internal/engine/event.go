package engine

import "time"

// EventKind classifies progress. The TUI and the headless CLI consume the same
// stream — one renders it as a moving list, the other as lines — so that what
// CI reports and what an operator watched are the same account of the run.
type EventKind string

// The kinds of event an operation reports.
const (
	EventStep     EventKind = "step"
	EventTable    EventKind = "table"
	EventProgress EventKind = "progress"
	EventWarning  EventKind = "warning"
	EventDone     EventKind = "done"
	EventFailed   EventKind = "failed"
)

// Event is one thing that happened.
type Event struct {
	Kind EventKind
	At   time.Time

	// Step names the phase: "dump", "filtered copy", "restore", "reindex".
	Step string

	// Database is which database the event is about, when an operation covers
	// more than one.
	//
	// Stamped once, by the operation, rather than written into every call site:
	// see Reporter.about. A snapshot of six databases is eighteen events named
	// dump, push and place, and without this the only clue which is which is a
	// path inside a message — so the run screen showed a reader eighteen
	// identical steps and they asked which database they were on.
	Database string

	// Table is set when the event is about one table.
	Table string

	Message string

	// Bytes and Rows are cumulative for the thing named, when known.
	Bytes int64
	Rows  int64

	Err error
}

// Reporter receives progress. A nil Reporter is valid and discards everything,
// so no caller has to supply one to run an operation.
type Reporter func(Event)

// about returns a Reporter that stamps a database on everything sent through
// it.
//
// One wrapper rather than a field on twenty call sites, and it covers what the
// operation calls into as well: the push, the placement and the dangling-trigger
// warnings all come out attributed without knowing they were being attributed.
// An event that already names a database keeps it, so an inner operation's
// answer wins over an outer one's.
func (r Reporter) about(database string) Reporter {
	if r == nil || database == "" {
		return r
	}
	return func(e Event) {
		if e.Database == "" {
			e.Database = database
		}
		r(e)
	}
}

func (r Reporter) send(e Event) {
	if r == nil {
		return
	}
	e.At = time.Now()
	r(e)
}

func (r Reporter) step(step, msg string) {
	r.send(Event{Kind: EventStep, Step: step, Message: msg})
}

func (r Reporter) warn(msg string) {
	r.send(Event{Kind: EventWarning, Message: msg})
}

func (r Reporter) table(step, table, msg string) {
	r.send(Event{Kind: EventTable, Step: step, Table: table, Message: msg})
}
