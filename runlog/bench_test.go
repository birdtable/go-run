package runlog

import (
	"bufio"
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// bigLog writes a log of about size bytes: runs of a record line, two changes of it and an entry line, as a
// program that journals each run writes them. It returns the log and the offset at the start of the last run.
func bigLog(tb testing.TB, size int) (Log, int64) {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "runs.jsonl")
	f, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}
	at := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	var n, last int64
	for i := 0; n < int64(size); i++ {
		last = n
		id := fmt.Sprintf("%016x", i)
		for _, line := range []string{
			fmt.Sprintf(`{"id":%q,"argv":["deploy","--env","prod","--run","%d"],"started":%q,"code":0,"changes":[]}`, id, i, at.Format(time.RFC3339Nano)),
			fmt.Sprintf(`{"entry":%q,"time":%q,"value":{"step":"fetch","bytes":%d}}`, id, at.Format(time.RFC3339Nano), i*7),
			fmt.Sprintf(`{"id":%q,"argv":["deploy","--env","prod","--run","%d"],"started":%q,"finished":%q,"code":0,"outcome":"done","summary":"deployed run %d to prod","changes":["a/b/%d.conf","c/d/%d.conf"]}`, id, i, at.Format(time.RFC3339Nano), at.Add(time.Second).Format(time.RFC3339Nano), i, i, i),
		} {
			m, err := fmt.Fprintln(f, line)
			if err != nil {
				tb.Fatal(err)
			}
			n += int64(m)
		}
	}
	if err := f.Close(); err != nil {
		tb.Fatal(err)
	}
	return Log{Path: path}, last
}

// BenchmarkReadTwoMB is Log.Read of a 2 MB log, beside a plain bufio.Scanner pass that decodes each line once.
func BenchmarkReadTwoMB(b *testing.B) {
	l, _ := bigLog(b, 2<<20)
	b.Run("LogRead", func(b *testing.B) {
		for b.Loop() {
			if _, err := l.Read(context.Background()); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("ScannerAndDecode", func(b *testing.B) {
		for b.Loop() {
			f, err := os.Open(l.Path)
			if err != nil {
				b.Fatal(err)
			}
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 0, 64<<10), DefaultMaxLine)
			var kept []Record // each decoded line is kept, as Log.Read keeps what it returns
			for sc.Scan() {
				var in struct {
					Record
					Rest map[string]jsontext.Value `json:",embed"`
				}
				if err := json.Unmarshal(sc.Bytes(), &in, opts()); err == nil {
					kept = append(kept, in.Record)
				}
			}
			if err := f.Close(); err != nil || sc.Err() != nil || len(kept) == 0 {
				b.Fatal(err, sc.Err())
			}
		}
	})
}
