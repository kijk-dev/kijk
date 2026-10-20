package agentstest

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
)

// FakeCodex is a Codex app server for tests: it answers from its threads, or plays recorded
// replies, and records each request.
type FakeCodex struct {
	Socket  string
	mu      sync.Mutex
	threads []map[string]any
	stored  []map[string]any // stored threads this app server has not loaded, listed before its own
	played  []Record
	reqs    []Request
	// reply, when set, answers instead of the normal handler. It may send nothing.
	reply func(c *Conn, id int, method string)
}

// Record is one request to a real Codex app server and its reply, as in
// internal/agents/testdata/codex/replies.json.
type Record struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Reply  json.RawMessage `json:"reply"`
}

// Request is one request the fake got.
type Request struct {
	Method string
	Params json.RawMessage
}

// A Codex agent runs in an app server, so a fake one serves it.
func init() {
	starters["codex"] = func(t *testing.T, home, cwd, name string) {
		NewFakeCodexAt(t, CodexSocket(filepath.Join(home, ".codex"))).Set(Thread("t1", cwd, "idle", "name", name))
	}
}

// ShortTempDir is for socket paths, which must stay under about 104 bytes; t.TempDir() and even
// $TMPDIR are longer on macOS.
func ShortTempDir(t *testing.T) string {
	t.Helper()
	base := "/tmp"
	if runtime.GOOS == "windows" {
		base = ""
	}
	dir, err := os.MkdirTemp(base, "kc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func NewFakeCodex(t *testing.T) *FakeCodex {
	t.Helper()
	return NewFakeCodexAt(t, filepath.Join(ShortTempDir(t), "s.sock"))
}

// CodexHome is a Codex home with a fake app server on the socket kijk looks for.
func CodexHome(t *testing.T) (string, *FakeCodex) {
	t.Helper()
	home := ShortTempDir(t)
	return home, NewFakeCodexAt(t, CodexSocket(home))
}

// CodexSocket is where the app server of a Codex home listens.
func CodexSocket(home string) string {
	return filepath.Join(home, "app-server-control", "app-server-control.sock")
}

// NewFakeCodexAt listens on socket, such as the one kijk looks for in a Codex home.
func NewFakeCodexAt(t *testing.T, socket string) *FakeCodex {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	f := &FakeCodex{Socket: socket}
	ln, err := net.Listen("unix", f.Socket)
	if err != nil {
		t.Skipf("no Unix sockets here: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(nc)
		}
	}()
	return f
}

// Set gives the threads this app server has loaded.
func (f *FakeCodex) Set(threads ...map[string]any) {
	f.mu.Lock()
	f.threads = threads
	f.mu.Unlock()
}

// Store adds a stored thread this app server has not loaded.
func (f *FakeCodex) Store(thread map[string]any) {
	f.mu.Lock()
	f.stored = append(f.stored, thread)
	f.mu.Unlock()
}

// Play answers each request with the recorded reply to the same method, and for thread/read the
// same thread.
func (f *FakeCodex) Play(records []Record) {
	f.mu.Lock()
	f.played = records
	f.mu.Unlock()
}

// Recording is the replies of a real Codex app server in internal/agents/testdata/codex.
func Recording() []Record {
	recording.Do(func() {
		b, err := os.ReadFile(filepath.Join(testdata(), "codex", "replies.json"))
		if err == nil {
			err = json.Unmarshal(b, &recorded)
		}
		if err != nil {
			panic(fmt.Sprintf("cannot read the Codex recording: %v", err))
		}
	})
	return recorded
}

var (
	recording sync.Once
	recorded  []Record
)

// Reply sets a handler that answers instead of the normal one; nil gives the normal answers back.
func (f *FakeCodex) Reply(reply func(c *Conn, id int, method string)) {
	f.mu.Lock()
	f.reply = reply
	f.mu.Unlock()
}

// Seen gives the methods of the requests so far.
func (f *FakeCodex) Seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var methods []string
	for _, r := range f.reqs {
		methods = append(methods, r.Method)
	}
	return methods
}

// Take gives the requests so far and forgets them.
func (f *FakeCodex) Take() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	reqs := f.reqs
	f.reqs = nil
	return reqs
}

// Conn is the server side of one connection.
type Conn struct {
	nc net.Conn
	r  *bufio.Reader
}

// Send writes one unmasked frame, as a server does.
func (c *Conn) Send(op byte, payload []byte) {
	hdr := []byte{0x80 | op}
	switch n := len(payload); {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n < 1<<16:
		hdr = append(hdr, 126)
		hdr = binary.BigEndian.AppendUint16(hdr, uint16(n))
	default:
		hdr = append(hdr, 127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	c.nc.Write(append(hdr, payload...))
}

func (c *Conn) JSON(v any) {
	b, _ := json.Marshal(v)
	c.Send(0x1, b)
}

// Write writes raw bytes, such as a broken frame.
func (c *Conn) Write(b []byte) {
	c.nc.Write(b)
}

// read returns the next message from the client, answering pings and joining fragments on the way.
func (c *Conn) read() ([]byte, error) {
	var msg []byte
	for {
		var h [2]byte
		if _, err := io.ReadFull(c.r, h[:]); err != nil {
			return nil, err
		}
		fin, op, n := h[0]&0x80 != 0, h[0]&0x0f, uint64(h[1]&0x7f)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(c.r, b[:]); err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(c.r, b[:]); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		var mask [4]byte
		if h[1]&0x80 != 0 {
			if _, err := io.ReadFull(c.r, mask[:]); err != nil {
				return nil, err
			}
		}
		if n > 16<<20 {
			return nil, errors.New("frame too large")
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(c.r, payload); err != nil {
			return nil, err
		}
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
		switch op {
		case 0x8:
			return nil, io.EOF
		case 0x9:
			c.Send(0xa, payload)
			continue
		case 0xa:
			continue
		}
		msg = append(msg, payload...)
		if fin {
			return msg, nil
		}
	}
}

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func (f *FakeCodex) serve(nc net.Conn) {
	defer nc.Close()
	r := bufio.NewReader(nc)
	req, err := http.ReadRequest(r)
	if err != nil {
		return
	}
	sum := sha1.Sum([]byte(req.Header.Get("Sec-WebSocket-Key") + wsGUID))
	nc.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n"))
	c := &Conn{nc, r}
	for {
		b, err := c.read()
		if err != nil {
			return
		}
		var m struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		json.Unmarshal(b, &m)
		f.mu.Lock()
		f.reqs = append(f.reqs, Request{m.Method, m.Params})
		reply, threads, stored, played := f.reply, f.threads, f.stored, f.played
		f.mu.Unlock()
		if reply != nil {
			reply(c, m.ID, m.Method)
			continue
		}
		if played != nil {
			c.JSON(playback(played, m.ID, m.Method, m.Params))
			continue
		}
		switch m.Method {
		case "initialize":
			c.JSON(map[string]any{"id": m.ID, "result": map[string]any{"userAgent": "fake"}})
		case "thread/loaded/list":
			ids := []string{}
			for _, t := range threads {
				ids = append(ids, t["id"].(string))
			}
			c.JSON(map[string]any{"id": m.ID, "result": map[string]any{"data": ids, "nextCursor": nil}})
		case "thread/list":
			// Codex stores a thread at its first prompt; only stored threads are listed.
			data := slices.Clone(stored)
			for _, t := range threads {
				if t["preview"] != "" {
					data = append(data, t)
				}
			}
			var p struct {
				Limit int `json:"limit"`
			}
			json.Unmarshal(m.Params, &p)
			data = data[:min(p.Limit, len(data))]
			c.JSON(map[string]any{"id": m.ID, "result": map[string]any{"data": data, "nextCursor": nil}})
		case "thread/read":
			var p struct {
				ThreadID string `json:"threadId"`
			}
			json.Unmarshal(m.Params, &p)
			for _, t := range threads {
				if t["id"] == p.ThreadID {
					c.JSON(map[string]any{"id": m.ID, "result": map[string]any{"thread": t}})
				}
			}
		default:
			c.JSON(map[string]any{"id": m.ID, "error": map[string]any{"code": -32601, "message": "unknown method"}})
		}
	}
}

// playback is the recorded reply to a request, with the request's id.
func playback(records []Record, id int, method string, params json.RawMessage) map[string]any {
	var thread struct {
		ThreadID string `json:"threadId"`
	}
	json.Unmarshal(params, &thread)
	for _, r := range records {
		var p struct {
			ThreadID string `json:"threadId"`
		}
		json.Unmarshal(r.Params, &p)
		if r.Method != method || p.ThreadID != thread.ThreadID {
			continue
		}
		var reply map[string]any
		json.Unmarshal(r.Reply, &reply)
		reply["id"] = id
		return reply
	}
	return map[string]any{"id": id, "error": map[string]any{"code": -32601, "message": "no recorded reply"}}
}

// Thread is a loaded thread as thread/read gives it: a recorded thread with the id, folder and
// status given, and extra field names and values to set.
func Thread(id, cwd, status string, extra ...any) map[string]any {
	var t map[string]any
	for _, r := range Recording() {
		var reply struct {
			Result struct {
				Thread map[string]any `json:"thread"`
			} `json:"result"`
		}
		if r.Method == "thread/read" && json.Unmarshal(r.Reply, &reply) == nil && reply.Result.Thread != nil {
			t = reply.Result.Thread
			break
		}
	}
	if t == nil {
		panic("the Codex recording has no thread/read reply")
	}
	maps.Copy(t, map[string]any{
		"id": id, "sessionId": id, "cwd": cwd, "name": nil, "preview": "prompt of " + id, "ephemeral": false,
		"parentThreadId": nil, "updatedAt": 1791994219, "status": map[string]any{"type": status},
		"futureField": map[string]any{"x": 1},
	})
	if status == "active" {
		t["status"] = map[string]any{"type": "active", "activeFlags": []string{}}
	}
	for i := 0; i+1 < len(extra); i += 2 {
		t[extra[i].(string)] = extra[i+1]
	}
	return t
}
