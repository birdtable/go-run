package runlog

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrNotFound is Find's error when no run has an id with the prefix it was given, and Resume's when none has the id.
	ErrNotFound = errors.New("run: no run has that id")
	// ErrAmbiguous is Find's error when more than one run has an id with the prefix.
	ErrAmbiguous = errors.New("run: more than one run has that id prefix")
)

// Find is the run whose id is idPrefix, or the one run whose id starts with it, so a person types a
// few characters of an id: ErrNotFound when no run's does, ErrAmbiguous when more than one's does.
func (l Log) Find(ctx context.Context, idPrefix string) (Record, error) {
	runs, err := l.Read(ctx)
	if err != nil {
		return Record{}, err
	}
	var found []Record
	for _, r := range runs {
		if r.ID == idPrefix {
			return r, nil
		}
		if idPrefix != "" && strings.HasPrefix(r.ID, idPrefix) {
			found = append(found, r)
		}
	}
	switch len(found) {
	case 0:
		return Record{}, fmt.Errorf("%w: %q", ErrNotFound, idPrefix)
	case 1:
		return found[0], nil
	}
	return Record{}, fmt.Errorf("%w: %q starts %d ids", ErrAmbiguous, idPrefix, len(found))
}

// Lines is the pure half of compaction: one record line for each run keep accepts, in order, each as Read
// gives it (a nil keep keeps every run and only folds its record lines into one), then the run's entry lines,
// and dropped, the runs keep
// refused, so the caller can remove their kept copies. It writes nothing and takes no lock: the
// caller replaces the log with the lines, under its own lock.
func Lines(runs []Record, keep func(Record) bool) (lines []byte, dropped []Record, err error) {
	for _, r := range runs {
		if keep != nil && !keep(r) {
			dropped = append(dropped, r)
			continue
		}
		b, err := marshal(r)
		for i := 0; err == nil && i < len(r.Entries); i++ {
			lines = append(append(lines, b...), '\n')
			b, err = marshal(entryLine{r.ID, r.Entries[i]})
		}
		if err != nil {
			return nil, nil, err
		}
		lines = append(append(lines, b...), '\n')
	}
	return lines, dropped, nil
}
