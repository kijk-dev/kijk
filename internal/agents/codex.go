package agents

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// All knowledge of the Codex app server protocol lives in this file. The `codex` TUI runs its
// threads in a shared app server, which speaks JSON-RPC in WebSocket text frames on a Unix socket.

// The OpenAI mark, from lobe-icons (MIT).
//
//go:embed codex.svg
var codexIcon string

func init() {
	add(AgentType{
		Name:     "codex",
		Label:    "Codex",
		Icon:     strings.TrimSpace(codexIcon),
		Sessions: codexSessions,
	})
}

const (
	codexTimeout  = 500 * time.Millisecond // per request
	maxCodexFrame = 16 << 20
	// Stored threads that this app server has not loaded, such as `codex exec` runs, also fill thread/list.
	codexListSlack = 50
)

// codexCache keeps the last read of each loaded thread, between scans.
type codexCache map[string]codexThread

// codexState is what the Codex reader keeps between scans.
type codexState struct {
	cache codexCache
	seen  statusClock
}

type codexThread struct {
	ID             string  `json:"id"`
	Cwd            string  `json:"cwd"`
	Name           *string `json:"name"`
	Preview        string  `json:"preview"`
	Ephemeral      bool    `json:"ephemeral"`
	ParentThreadID *string `json:"parentThreadId"`
	UpdatedAt      int64   `json:"updatedAt"` // unix seconds
	Status         struct {
		Type        string   `json:"type"`
		ActiveFlags []string `json:"activeFlags"`
	} `json:"status"`
}

// busy is false for an active thread that waits on the user: it has stopped working.
func (t codexThread) busy() bool {
	f := t.Status.ActiveFlags
	return t.Status.Type == "active" && !slices.Contains(f, "waitingOnApproval") && !slices.Contains(f, "waitingOnUserInput")
}

func codexSocket(home string) string {
	return filepath.Join(home, "app-server-control", "app-server-control.sock")
}

// readCodexThreads returns the threads loaded in the app server; none when no Codex TUI runs. It
// closes the connection, because Codex attaches each open connection to new threads and keeps them loaded.
func readCodexThreads(socket string, cache codexCache, version string) ([]codexThread, error) {
	nc, err := net.DialTimeout("unix", socket, codexTimeout)
	// Checks the file, as Windows reports a missing socket as a dead network.
	if err != nil && (!exists(socket) || errors.Is(err, errConnRefused)) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot reach the Codex app server: %w", err)
	}
	defer nc.Close()
	nc.SetDeadline(time.Now().Add(codexTimeout))
	c, err := upgrade(nc)
	if err != nil {
		return nil, err
	}
	if err := c.initialize(version); err != nil {
		return nil, err
	}
	ids, err := c.loadedThreads()
	if err != nil {
		return nil, err
	}
	// A thread before its first prompt is slow to read, as Codex searches the disk for it. It is
	// read again only once it is stored.
	var read []string
	waiting := map[string]bool{}
	for _, id := range ids {
		if t, ok := cache[id]; ok && t.Preview == "" {
			waiting[id] = true
		} else {
			read = append(read, id)
		}
	}
	if len(waiting) > 0 {
		stored, err := c.recentThreads(len(ids) + codexListSlack)
		if err != nil {
			return nil, err
		}
		for _, id := range stored {
			if waiting[id] {
				read = append(read, id)
			}
		}
	}
	for _, id := range read {
		t, err := c.readThread(id)
		if err != nil {
			return nil, err
		}
		cache[id] = t
	}
	loaded := map[string]bool{}
	threads := make([]codexThread, 0, len(ids))
	for _, id := range ids {
		loaded[id] = true
		threads = append(threads, cache[id])
	}
	for id := range cache {
		if !loaded[id] {
			delete(cache, id)
		}
	}
	return threads, nil
}

func codexSessions(env *Env) ([]Session, error) {
	home, ok := env.folder("CODEX_HOME", ".codex")
	if !ok {
		return nil, nil
	}
	st := state(env, "codex", func() *codexState { return &codexState{cache: codexCache{}, seen: statusClock{}} })
	threads, err := readCodexThreads(codexSocket(home), st.cache, env.Version)
	if err != nil {
		return nil, err
	}
	var sessions []Session
	loaded := map[string]bool{}
	for _, t := range threads {
		loaded[t.ID] = true
		if t.Ephemeral || t.ParentThreadID != nil {
			continue // hidden helpers and sub-agents
		}
		sessions = append(sessions, st.session(t))
	}
	st.seen.keep(loaded)
	return sessions, nil
}

// The app server gives no time for a status change, so kijk notes when it sees one.
func (st *codexState) session(t codexThread) Session {
	last := st.seen.since(t.ID, t.Status.Type+fmt.Sprint(t.Status.ActiveFlags), time.Unix(t.UpdatedAt, 0))
	if t.busy() {
		last = time.Now()
	}
	name := t.Preview
	if t.Name != nil && *t.Name != "" {
		name = *t.Name
	}
	if name == "" {
		name = t.ID[:min(8, len(t.ID))]
	}
	return Session{Name: name, Cwd: t.Cwd, Busy: t.busy(), Last: last}
}

// codexConn offers only the requests that read; the same socket can start turns and approve commands.
type codexConn struct {
	nc   net.Conn
	r    *bufio.Reader
	next int
}

func (c *codexConn) initialize(version string) error {
	params := map[string]any{"clientInfo": map[string]string{"name": "kijk", "version": version}}
	return c.call("initialize", params, nil)
}

func (c *codexConn) loadedThreads() ([]string, error) {
	var ids []string
	params := map[string]any{}
	for {
		var res struct {
			Data       []string `json:"data"`
			NextCursor *string  `json:"nextCursor"`
		}
		if err := c.call("thread/loaded/list", params, &res); err != nil {
			return nil, err
		}
		ids = append(ids, res.Data...)
		if res.NextCursor == nil || *res.NextCursor == "" {
			return ids, nil
		}
		params = map[string]any{"cursor": *res.NextCursor}
	}
}

// recentThreads returns the ids of the n stored threads updated last, from the state database only.
func (c *codexConn) recentThreads(n int) ([]string, error) {
	var res struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	params := map[string]any{"limit": n, "sortKey": "updated_at", "useStateDbOnly": true}
	if err := c.call("thread/list", params, &res); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(res.Data))
	for _, t := range res.Data {
		ids = append(ids, t.ID)
	}
	return ids, nil
}

func (c *codexConn) readThread(id string) (codexThread, error) {
	var res struct {
		Thread *codexThread `json:"thread"`
	}
	if err := c.call("thread/read", map[string]any{"threadId": id, "includeTurns": false}, &res); err != nil {
		return codexThread{}, err
	}
	if res.Thread == nil || res.Thread.ID == "" {
		return codexThread{}, fmt.Errorf("thread/read for %s returned no thread", id)
	}
	return *res.Thread, nil
}

func (c *codexConn) call(method string, params, result any) error {
	c.nc.SetDeadline(time.Now().Add(codexTimeout))
	c.next++
	req, _ := json.Marshal(map[string]any{"id": c.next, "method": method, "params": params})
	if err := c.write(0x1, req); err != nil {
		return err
	}
	for {
		b, err := c.read()
		if err != nil {
			return err
		}
		var res struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(b, &res); err != nil {
			return fmt.Errorf("unreadable reply to %s: %w", method, err)
		}
		if res.ID == nil || *res.ID != c.next {
			continue // a notification
		}
		if res.Error != nil {
			return fmt.Errorf("%s failed: %s", method, res.Error.Message)
		}
		if result == nil {
			return nil
		}
		if err := json.Unmarshal(res.Result, result); err != nil {
			return fmt.Errorf("unreadable reply to %s: %w", method, err)
		}
		return nil
	}
}

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func upgrade(nc net.Conn) (*codexConn, error) {
	k := make([]byte, 16)
	rand.Read(k)
	key := base64.StdEncoding.EncodeToString(k)
	fmt.Fprintf(nc, "GET / HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", key)
	r := bufio.NewReader(nc)
	resp, err := http.ReadResponse(r, nil)
	if err != nil {
		return nil, fmt.Errorf("no WebSocket answer: %w", err)
	}
	resp.Body.Close()
	sum := sha1.Sum([]byte(key + wsGUID))
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		return nil, fmt.Errorf("no WebSocket answer: %s", resp.Status)
	}
	return &codexConn{nc: nc, r: r}, nil
}

// write sends one frame. A client masks every frame it sends.
func (c *codexConn) write(op byte, payload []byte) error {
	hdr := []byte{0x80 | op}
	switch n := len(payload); {
	case n < 126:
		hdr = append(hdr, 0x80|byte(n))
	case n < 1<<16:
		hdr = append(hdr, 0x80|126)
		hdr = binary.BigEndian.AppendUint16(hdr, uint16(n))
	default:
		hdr = append(hdr, 0x80|127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	mask := make([]byte, 4)
	rand.Read(mask)
	b := append(hdr, mask...)
	for i, x := range payload {
		b = append(b, x^mask[i%4])
	}
	_, err := c.nc.Write(b)
	return err
}

// read returns the next text message, answering pings and joining fragments on the way.
func (c *codexConn) read() ([]byte, error) {
	var msg []byte
	for {
		var h [2]byte
		if _, err := io.ReadFull(c.r, h[:]); err != nil {
			return nil, err
		}
		fin, op, masked := h[0]&0x80 != 0, h[0]&0x0f, h[1]&0x80 != 0
		n := uint64(h[1] & 0x7f)
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
		if n > maxCodexFrame-uint64(len(msg)) {
			return nil, errors.New("Codex app server message too large")
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(c.r, mask[:]); err != nil {
				return nil, err
			}
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(c.r, payload); err != nil {
			return nil, err
		}
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
		switch op {
		case 0x8:
			return nil, errors.New("Codex app server closed the connection")
		case 0x9:
			if err := c.write(0xa, payload); err != nil {
				return nil, err
			}
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
