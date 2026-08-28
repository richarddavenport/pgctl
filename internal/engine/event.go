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
