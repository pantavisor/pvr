//
// Copyright 2026  Pantacor Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package device

// pvr device live-logs: stream a device's log files through the Hub, as the
// Hub UI's Live logs tab does. Contract: pv-mqtt-sdk docs/logs.md.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/urfave/cli"
	"golang.org/x/term"
)

const (
	listLogSourcesCmd = "LIST_LOG_SOURCES"
	currentRev        = "current"

	maxLogSources      = 10
	maxSourcePath      = 512
	maxListedSources   = 2000
	defaultLogTail     = 200
	maxLogTail         = 500
	maxLogFilterBytes  = 256
	linesPollGap       = 250 * time.Millisecond
	linesIdleGap       = time.Second
	linesMaxBackoff    = 8 * time.Second
	sessionStopTimeout = 10 * time.Second
)

// Options

type liveLogsOptions struct {
	rev     string
	sources []string
	tail    int
	filter  string
	follow  bool
	all     bool
	json    bool
}

// validLogPath: a relative path of plain segments, as the device lists them.
func validLogPath(p string) bool {
	if p == "" || len(p) > maxSourcePath || strings.HasPrefix(p, "/") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// validateTail: the initial tail is 0 to maxLogTail lines.
func validateTail(n int) error {
	if n < 0 || n > maxLogTail {
		return fmt.Errorf("--tail must be between 0 and %d", maxLogTail)
	}
	return nil
}

// normalizeFilter trims the filter and checks it as the device does: at most
// 256 bytes of valid UTF-8, no control characters.
func normalizeFilter(f string) (string, error) {
	f = strings.TrimSpace(f)
	if f == "" {
		return "", nil
	}
	if !utf8.ValidString(f) {
		return "", errors.New("--filter must be valid UTF-8")
	}
	if len(f) > maxLogFilterBytes {
		return "", fmt.Errorf("--filter is too long: at most %d bytes", maxLogFilterBytes)
	}
	for _, r := range f {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return "", errors.New("--filter cannot contain control characters")
		}
	}
	return f, nil
}

// parseLiveLogsOptions validates the flags and the source arguments.
func parseLiveLogsOptions(rev string, sources []string, tail int, filter string, noFollow, all, asJSON bool) (*liveLogsOptions, error) {
	o := &liveLogsOptions{rev: strings.TrimSpace(rev), tail: tail, all: all, json: asJSON}
	if o.rev == "" {
		o.rev = currentRev
	}
	if err := validateTail(tail); err != nil {
		return nil, err
	}
	f, err := normalizeFilter(filter)
	if err != nil {
		return nil, err
	}
	o.filter = f
	if all && len(sources) > 0 {
		return nil, errors.New("--all streams the whole revision: give no sources with it")
	}
	if len(sources) > maxLogSources {
		return nil, fmt.Errorf("at most %d sources per session", maxLogSources)
	}
	seen := map[string]bool{}
	for _, s := range sources {
		s = strings.TrimSuffix(s, "/")
		if !validLogPath(s) {
			return nil, fmt.Errorf("invalid source %q: a path relative to the revision's log folder, without '..'", s)
		}
		if !seen[s] {
			seen[s] = true
			o.sources = append(o.sources, s)
		}
	}
	if all {
		o.sources = []string{""}
	}
	// Only the running revision is followed; any other one is read once.
	o.follow = !noFollow && o.rev == currentRev
	return o, nil
}

// Listing (LIST_LOG_SOURCES)

type logSource struct {
	Path string
	Size int64 // -1 when unknown
	Gz   bool
}

type logListing struct {
	Rev       string
	Revisions []string
	Sources   []logSource
	Truncated bool
}

// parseLogListing reads the output of LIST_LOG_SOURCES. Paths that are not
// plain relative paths are left out, a path listed twice is kept once.
func parseLogListing(raw []byte) (*logListing, error) {
	var v struct {
		Rev       string   `json:"rev"`
		Revisions []string `json:"revisions"`
		Sources   []struct {
			Path string   `json:"path"`
			Size *float64 `json:"size"`
			Gz   bool     `json:"gz"`
		} `json:"sources"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("unexpected LIST_LOG_SOURCES output: %v", err)
	}
	l := &logListing{Rev: v.Rev, Truncated: v.Truncated}
	if l.Rev == "" {
		l.Rev = currentRev
	}
	for _, r := range v.Revisions {
		if r != "" {
			l.Revisions = append(l.Revisions, r)
		}
	}
	seen := map[string]bool{}
	for _, s := range v.Sources {
		if !validLogPath(s.Path) || seen[s.Path] {
			continue
		}
		if len(l.Sources) >= maxListedSources {
			l.Truncated = true
			break
		}
		seen[s.Path] = true
		size := int64(-1)
		if s.Size != nil && *s.Size >= 0 {
			size = int64(*s.Size)
		}
		l.Sources = append(l.Sources, logSource{Path: s.Path, Size: size, Gz: s.Gz || strings.HasSuffix(s.Path, ".gz")})
	}
	return l, nil
}

type sourceNode struct {
	name     string
	dir      bool
	source   logSource
	children []*sourceNode
}

// buildSourceTree nests the paths: folders first, then files, each by name
// with numbers compared as numbers (`.log.2.gz` before `.log.10.gz`).
func buildSourceTree(sources []logSource) []*sourceNode {
	root := &sourceNode{dir: true}
	dirs := map[string]*sourceNode{"": root}
	for _, s := range sources {
		parts := strings.Split(s.Path, "/")
		node, p := root, ""
		for _, part := range parts[:len(parts)-1] {
			if p == "" {
				p = part
			} else {
				p += "/" + part
			}
			next := dirs[p]
			if next == nil {
				next = &sourceNode{name: part, dir: true}
				dirs[p] = next
				node.children = append(node.children, next)
			}
			node = next
		}
		node.children = append(node.children, &sourceNode{name: parts[len(parts)-1], source: s})
	}
	var sortTree func(n *sourceNode)
	sortTree = func(n *sourceNode) {
		sort.SliceStable(n.children, func(i, j int) bool {
			a, b := n.children[i], n.children[j]
			if a.dir != b.dir {
				return a.dir
			}
			return naturalLess(a.name, b.name)
		})
		for _, c := range n.children {
			if c.dir {
				sortTree(c)
			}
		}
	}
	sortTree(root)
	return root.children
}

// naturalLess compares strings with runs of digits compared as numbers.
func naturalLess(a, b string) bool {
	for a != "" && b != "" {
		da, db := leadingDigits(a), leadingDigits(b)
		if da != "" && db != "" {
			na, _ := strconv.ParseUint(strings.TrimLeft(da, "0")+"0", 10, 64)
			nb, _ := strconv.ParseUint(strings.TrimLeft(db, "0")+"0", 10, 64)
			if na != nb {
				return na < nb
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		ra, sa := utf8.DecodeRuneInString(a)
		rb, sb := utf8.DecodeRuneInString(b)
		if ra != rb {
			return ra < rb
		}
		a, b = a[sa:], b[sb:]
	}
	return len(a) < len(b)
}

func leadingDigits(s string) string {
	i := 0
	for i < len(s) && i < 18 && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

// formatSize is a byte count for people.
func formatSize(n int64) string {
	switch {
	case n < 0:
		return ""
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
	}
}

// printSourceTree prints the listing as an indented tree: folders end with
// '/', files show their size and `gz` when rotated.
func printSourceTree(w io.Writer, l *logListing) {
	fmt.Fprintf(w, "Revision: %s\n", shownText(l.Rev))
	var others []string
	for _, r := range l.Revisions {
		if r != l.Rev {
			others = append(others, shownText(r))
		}
	}
	if len(others) > 0 {
		fmt.Fprintf(w, "Other revisions: %s\n", strings.Join(others, ", "))
	}
	if len(l.Sources) == 0 {
		fmt.Fprintln(w, "No log files.")
		return
	}
	tree := buildSourceTree(l.Sources)
	type row struct{ label, size, gz string }
	var rows []row
	var walk func(nodes []*sourceNode, depth int)
	walk = func(nodes []*sourceNode, depth int) {
		for _, n := range nodes {
			label := strings.Repeat("  ", depth) + shownText(n.name)
			if n.dir {
				rows = append(rows, row{label: label + "/"})
				walk(n.children, depth+1)
				continue
			}
			gz := ""
			if n.source.Gz {
				gz = "gz"
			}
			rows = append(rows, row{label: label, size: formatSize(n.source.Size), gz: gz})
		}
	}
	walk(tree, 0)
	width := 0
	for _, r := range rows {
		if n := utf8.RuneCountInString(r.label); n > width {
			width = n
		}
	}
	for _, r := range rows {
		if r.size == "" && r.gz == "" {
			fmt.Fprintln(w, r.label)
			continue
		}
		pad := width - utf8.RuneCountInString(r.label)
		line := fmt.Sprintf("%s%s  %10s", r.label, strings.Repeat(" ", pad), r.size)
		if r.gz != "" {
			line += "  " + r.gz
		}
		fmt.Fprintln(w, line)
	}
	if l.Truncated {
		fmt.Fprintf(w, "(listing truncated: the device lists at most %d files)\n", maxListedSources)
	}
}

// Sessions

type logSessionRequest struct {
	Rev     string   `json:"rev"`
	Sources []string `json:"sources"`
	Tail    int      `json:"tail"`
	Follow  bool     `json:"follow"`
	Filter  string   `json:"filter,omitempty"`
}

type logSession struct {
	ID        string    `json:"id"`
	ExpiresAt time.Time `json:"expires_at"`
	Deadline  time.Time `json:"deadline"`
}

type logLine struct {
	Src  string `json:"src"`
	Line string `json:"line"`
}

type logBatch struct {
	Seq     int64     `json:"seq"`
	Lines   []logLine `json:"lines"`
	Dropped int64     `json:"dropped"`
	End     bool      `json:"end"`
	Reason  string    `json:"reason"`
}

type logLinesView struct {
	Batches []logBatch `json:"batches"`
	Next    *int64     `json:"next"`
	Ended   bool       `json:"ended"`
	Reason  string     `json:"reason"`
}

func (h *hubClient) createLogSession(ctx context.Context, req logSessionRequest) (*logSession, error) {
	var s logSession
	err := h.doJSON(ctx, "starting the log session", http.MethodPost, h.deviceURL("log-sessions"), req, &s, http.StatusCreated, http.StatusOK)
	if err != nil {
		return nil, describeSessionError(err)
	}
	if s.ID == "" {
		return nil, errors.New("starting the log session: the API answered no session id")
	}
	return &s, nil
}

func (h *hubClient) renewLogSession(ctx context.Context, sid string) error {
	err := h.doJSON(ctx, "renewing the log session", http.MethodPost, h.deviceURL("log-sessions", sid, "renew"), struct{}{}, nil, http.StatusOK)
	if statusOf(err) == http.StatusGone {
		return errSessionGone
	}
	return err
}

func (h *hubClient) deleteLogSession(ctx context.Context, sid string) error {
	return h.doJSON(ctx, "stopping the log session", http.MethodDelete, h.deviceURL("log-sessions", sid), nil, nil, http.StatusNoContent, http.StatusOK, http.StatusNotFound, http.StatusGone)
}

func (h *hubClient) logSessionLines(ctx context.Context, sid string, after int64) (*logLinesView, error) {
	u := h.deviceURL("log-sessions", sid, "lines") + "?after=" + strconv.FormatInt(after, 10)
	var v logLinesView
	if err := h.doJSON(ctx, "reading the log lines", http.MethodGet, u, nil, &v, http.StatusOK); err != nil {
		return nil, err
	}
	return &v, nil
}

// lineStream folds the long-poll answers: batches in seq order, each once;
// a jump in seq (batches go over MQTT QoS 0) is a gap.
type lineStream struct {
	after   int64
	lastSeq int64
	lost    int64
	dropped int64
	ended   bool
	reason  string
}

func newLineStream() *lineStream { return &lineStream{after: -1, lastSeq: -1} }

// streamEvents receives what apply finds.
type streamEvents interface {
	line(seq int64, l logLine)
	gap(lost int64)
	dropped(n int64)
}

// apply folds one answer and returns how many lines it brought.
func (s *lineStream) apply(v *logLinesView, ev streamEvents) int {
	batches := append([]logBatch(nil), v.Batches...)
	sort.SliceStable(batches, func(i, j int) bool { return batches[i].Seq < batches[j].Seq })
	n := 0
	for _, b := range batches {
		if b.Seq <= s.lastSeq {
			continue
		}
		if lost := b.Seq - s.lastSeq - 1; lost > 0 {
			s.lost += lost
			ev.gap(lost)
		}
		for _, l := range b.Lines {
			ev.line(b.Seq, l)
			n++
		}
		if b.Dropped > 0 {
			s.dropped += b.Dropped
			ev.dropped(b.Dropped)
		}
		s.lastSeq = b.Seq
		if b.End {
			s.ended = true
			if b.Reason != "" {
				s.reason = b.Reason
			}
		}
	}
	if v.Ended {
		s.ended = true
		if v.Reason != "" {
			s.reason = v.Reason
		}
	}
	next := s.after
	if v.Next != nil {
		next = *v.Next
	}
	if s.lastSeq > next {
		next = s.lastSeq
	}
	s.after = next
	return n
}

// Output

// ANSI colours for the sources, picked by a hash of the path.
var srcColors = []string{"31", "32", "33", "34", "35", "36", "91", "92", "93", "94", "95", "96"}

type linePrinter struct {
	out    io.Writer
	errOut io.Writer
	json   bool
	color  bool // stdout is a terminal: colour the source, neutralise control characters
	mu     sync.Mutex
}

func (p *linePrinter) line(seq int64, l logLine) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.json {
		b, _ := json.Marshal(struct {
			Src  string `json:"src"`
			Line string `json:"line"`
			Seq  int64  `json:"seq"`
		}{l.Src, l.Line, seq})
		fmt.Fprintf(p.out, "%s\n", b)
		return
	}
	if l.Src == "" {
		// the device's own notice, e.g. that a folder has more files than it streams
		fmt.Fprintf(p.errOut, "device: %s\n", shownText(l.Line))
		return
	}
	src, text := l.Src, l.Line
	if p.color {
		src, text = terminalSafe(src), terminalSafe(text)
		fmt.Fprintf(p.out, "\x1b[%sm[%s]\x1b[0m %s\n", srcColor(l.Src), src, text)
		return
	}
	fmt.Fprintf(p.out, "[%s] %s\n", src, text)
}

func (p *linePrinter) gap(lost int64) {
	fmt.Fprintf(p.errOut, "pvr: %s did not arrive: some lines were lost\n", plural(lost, "batch", "batches"))
}

func (p *linePrinter) dropped(n int64) {
	fmt.Fprintf(p.errOut, "pvr: %s dropped by the device (over its 64 KiB/s limit)\n", plural(n, "line was", "lines were"))
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func srcColor(src string) string {
	h := fnv.New32a()
	h.Write([]byte(src))
	return srcColors[h.Sum32()%uint32(len(srcColors))]
}

// terminalSafe replaces control characters (except tab) so a log line
// cannot drive the terminal with escape sequences.
func terminalSafe(s string) string {
	clean := true
	for _, r := range s {
		if isControl(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	return strings.Map(func(r rune) rune {
		if isControl(r) {
			return unicode.ReplacementChar
		}
		return r
	}, s)
}

// Running a session

type liveLogsRunner struct {
	api        *hubClient
	req        logSessionRequest
	printer    *linePrinter
	errOut     io.Writer
	renewEvery time.Duration
	pollGap    time.Duration
	idleGap    time.Duration
	maxBackoff time.Duration
}

func newLiveLogsRunner(api *hubClient, req logSessionRequest, printer *linePrinter) *liveLogsRunner {
	return &liveLogsRunner{
		api:        api,
		req:        req,
		printer:    printer,
		errOut:     printer.errOut,
		renewEvery: renewInterval,
		pollGap:    linesPollGap,
		idleGap:    linesIdleGap,
		maxBackoff: linesMaxBackoff,
	}
}

// run starts the session, renews its lease and prints its lines until it
// ends (answering the end reason) or ctx is cancelled; then, or on a fatal
// error, it stops the session before returning.
func (r *liveLogsRunner) run(ctx context.Context) (string, error) {
	sess, err := r.api.createLogSession(ctx, r.req)
	if err != nil {
		return "", err
	}
	renewCtx, stopRenew := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		renewLease(renewCtx, r.renewEvery, func(c context.Context) error {
			return r.api.renewLogSession(c, sess.ID)
		}, func(err error) {
			fmt.Fprintf(r.errOut, "pvr: %v\n", err)
		})
	}()
	reason, err := r.poll(ctx, sess.ID)
	stopRenew()
	wg.Wait()
	if err != nil {
		dctx, cancel := context.WithTimeout(context.Background(), sessionStopTimeout)
		defer cancel()
		if derr := r.api.deleteLogSession(dctx, sess.ID); derr != nil {
			fmt.Fprintf(r.errOut, "pvr: %v (the session ends with its lease)\n", derr)
		}
		return "", err
	}
	return reason, nil
}

// poll long-polls the lines until the session ends.
func (r *liveLogsRunner) poll(ctx context.Context, sid string) (string, error) {
	stream := newLineStream()
	failures := 0
	for {
		view, err := r.api.logSessionLines(ctx, sid, stream.after)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err != nil {
			status := statusOf(err)
			if status == http.StatusGone {
				return "stopped", nil
			}
			if !retryable(status) {
				return "", err
			}
			failures++
			if failures == 1 || failures%5 == 0 {
				fmt.Fprintf(r.errOut, "pvr: %v (retrying)\n", err)
			}
			if !sleepCtx(ctx, backoff(failures, r.idleGap, r.maxBackoff)) {
				return "", ctx.Err()
			}
			continue
		}
		failures = 0
		n := stream.apply(view, r.printer)
		if stream.ended {
			return stream.reason, nil
		}
		gap := r.idleGap
		if n > 0 || len(view.Batches) > 0 {
			gap = r.pollGap
		}
		if !sleepCtx(ctx, gap) {
			return "", ctx.Err()
		}
	}
}

// logEndReasons: why a session ended (docs/logs.md), and whether that is a
// failure.
var logEndReasons = map[string]struct {
	text string
	fail bool
}{
	"done":     {"finished: the sources were read", false},
	"stopped":  {"the session was stopped", false},
	"deadline": {"the session reached its 30 minute limit", false},
	"expired":  {"the session's lease ran out (renewals failed)", true},
	"busy":     {"the device is already streaming as many sessions as it allows", true},
	"invalid":  {"the device refused the request (unknown revision or sources)", true},
}

// describeLogEnd says why the session ended and whether it failed.
func describeLogEnd(reason string) (string, bool) {
	if reason == "" {
		return "the session ended", false
	}
	if d, ok := logEndReasons[reason]; ok {
		return d.text, d.fail
	}
	for key, d := range logEndReasons {
		if strings.HasPrefix(reason, key+":") {
			return d.text + " (" + shownText(reason) + ")", d.fail
		}
	}
	return "the session ended: " + shownText(reason), true
}

// Command

func CommandLiveLogs() cli.Command {
	return cli.Command{
		Name:           "live-logs",
		Aliases:        []string{"livelogs"},
		ArgsUsage:      "<NICK|ID> [SOURCE...]",
		Usage:          "Stream a device's log files live through Pantahub",
		SkipArgReorder: true,
		Description: helpText(`Streams log files of a device connected to Pantahub over MQTT (pv-mqtt-sdk),
as the Hub UI's Live logs tab does. Sources are paths relative to the
revision's log folder (/pantavisor/logs/<rev>/), e.g. pantavisor/pantavisor.log
or <container>/lxc/console.log; a folder streams every live file under it.

Without sources, lists the revision's log files and exits.

   pvr device live-logs mydevice
   pvr device live-logs mydevice pantavisor/pantavisor.log os/lxc/console.log
   pvr device live-logs mydevice --filter error --tail 50 pantavisor
   pvr device live-logs mydevice --all --no-follow
   pvr device live-logs mydevice --rev locals/hub-3 pantavisor/pantavisor.log
   pvr device live-logs mydevice --json pantavisor/pantavisor.log | jq -r .line

Lines print as "[source] line". A session lives at most 30 minutes; pvr renews
its lease while it runs and stops it on Ctrl-C. Lost batches and lines the
device dropped (over 64 KiB/s) are reported on stderr. The exit status is 0
when the session finishes or is stopped, 1 when the device refuses or ends it
with an error, 130 on Ctrl-C.`),
		BashComplete: suggestDevices,
		Action: func(c *cli.Context) error {
			args, help, err := commandArgs(c)
			if err != nil {
				return cli.NewExitError(err.Error(), 2)
			}
			if help {
				return cli.ShowCommandHelp(c, c.Command.Name)
			}
			if len(args) < 1 {
				return cli.NewExitError("Device ID or Nick is required. See --help", 2)
			}
			opts, err := parseLiveLogsOptions(c.String("rev"), args[1:], c.Int("tail"), c.String("filter"),
				c.Bool("no-follow"), c.Bool("all"), c.Bool("json"))
			if err != nil {
				return cli.NewExitError(err.Error(), 2)
			}
			_, api, info, err := deviceClient(c, args[0])
			if err != nil {
				return cli.NewExitError(err.Error(), 4)
			}
			if !c.IsSet("rev") && info.Rev != "" {
				opts.rev = info.Rev
				opts.follow = false
			}

			ctx, received, stop := signalContext(context.Background())
			defer stop()

			if len(opts.sources) == 0 {
				return listLogSources(ctx, api, opts, os.Stdout, received)
			}

			isTTY := term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("NO_COLOR") == ""
			printer := &linePrinter{out: os.Stdout, errOut: os.Stderr, json: opts.json, color: isTTY && !opts.json}
			req := logSessionRequest{Rev: opts.rev, Sources: opts.sources, Tail: opts.tail, Follow: opts.follow, Filter: opts.filter}
			reason, err := newLiveLogsRunner(api, req, printer).run(ctx)
			if err != nil {
				if s := received(); s != nil {
					return cli.NewExitError("", signalExitCode(s))
				}
				return cli.NewExitError(err.Error(), 4)
			}
			text, failed := describeLogEnd(reason)
			if failed {
				return cli.NewExitError("pvr: "+text, 1)
			}
			if reason != "done" {
				fmt.Fprintf(os.Stderr, "pvr: %s\n", text)
			}
			return nil
		},
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:  "rev, r",
				Usage: "revision whose logs to read: current (followed live) or another revision name (read once)",
				Value: currentRev,
			},
			cli.IntFlag{
				Name:  "tail, n",
				Usage: fmt.Sprintf("lines read back from each source before following, 0 to %d (with --no-follow, 0 reads everything up to 4 MiB)", maxLogTail),
				Value: defaultLogTail,
			},
			cli.StringFlag{
				Name:  "filter, f",
				Usage: fmt.Sprintf("only lines containing this text, compared case-insensitively on the device (at most %d bytes)", maxLogFilterBytes),
			},
			cli.BoolFlag{
				Name:  "no-follow",
				Usage: "read the sources once and exit instead of following them",
			},
			cli.BoolFlag{
				Name:  "all, a",
				Usage: "stream the whole revision (its live files, at most 64)",
			},
			cli.BoolFlag{
				Name:  "json",
				Usage: `print one JSON object per line: {"src","line","seq"} (with no sources: the raw listing)`,
			},
		},
	}
}

// listLogSources runs LIST_LOG_SOURCES and prints the tree (or the listing
// as JSON).
func listLogSources(ctx context.Context, api *hubClient, opts *liveLogsOptions, out io.Writer, received func() os.Signal) error {
	raw, err := api.runCommand(ctx, listLogSourcesCmd, map[string]interface{}{"rev": opts.rev}, commandPollInterval, commandMaxWait)
	if err != nil {
		if s := received(); s != nil {
			return cli.NewExitError("", signalExitCode(s))
		}
		return cli.NewExitError(err.Error(), 4)
	}
	listing, err := parseLogListing(raw)
	if err != nil {
		return cli.NewExitError(err.Error(), 4)
	}
	if opts.json {
		fmt.Fprintf(out, "%s\n", strings.TrimSpace(string(raw)))
		return nil
	}
	printSourceTree(out, listing)
	fmt.Fprintln(os.Stderr, "\nStream sources with: pvr device live-logs <device> <path>... (a folder streams its live files; --all the whole revision)")
	return nil
}
