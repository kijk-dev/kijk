package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"slices"
	"strings"
	"text/tabwriter"
	"time"
)

var errWindows = errors.New("remote machines are not supported on Windows yet")

const machineUsage = `Usage:
  kijk machine add <ssh-target>   save a machine, such as an alias from ~/.ssh/config or user@host
  kijk machine login <machine>    log in to a machine in this terminal
  kijk machine list               show the saved machines and their states
  kijk machine remove <machine>   forget a machine
  kijk machine disable <machine>  stop connecting to a machine
  kijk machine enable <machine>   connect to a machine again`

// runMachine is the kijk machine command. It returns the exit code.
func runMachine(args []string, in io.Reader, out, errOut io.Writer) int {
	if runtime.GOOS == "windows" {
		fmt.Fprintln(errOut, "kijk machine:", errWindows)
		return 1
	}
	if len(args) == 0 {
		fmt.Fprintln(errOut, machineUsage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	want := 1
	if cmd == "list" {
		want = 0
	}
	if !slices.Contains([]string{"add", "login", "list", "remove", "disable", "enable"}, cmd) || len(rest) != want {
		fmt.Fprintln(errOut, machineUsage)
		return 2
	}
	var err error
	switch cmd {
	case "add":
		err = machineAdd(rest[0], in, out)
	case "login":
		err = machineLogin(rest[0], out)
	case "list":
		err = machineList(out)
	case "remove":
		err = machineRemove(rest[0], out)
	case "disable":
		err = machineEnable(rest[0], false, out)
	case "enable":
		err = machineEnable(rest[0], true, out)
	}
	if err != nil {
		fmt.Fprintf(errOut, "kijk machine %s: %v\n", cmd, err)
		return 1
	}
	return 0
}

// checkTarget refuses what ssh would read as an option, and what does not fit in a URL path.
func checkTarget(target string) error {
	if target == "" || strings.HasPrefix(target, "-") || strings.ContainsAny(target, " \t\r\n/\\'\"`$;&|<>") {
		return fmt.Errorf("%q is not an SSH target. Use a Host alias from ~/.ssh/config or user@host", target)
	}
	return nil
}

func findSaved(name string) (savedMachine, error) {
	list, err := savedMachines()
	if err != nil {
		return savedMachine{}, err
	}
	for _, m := range list {
		if m.Target == name {
			return m, nil
		}
	}
	return savedMachine{}, fmt.Errorf("no machine is saved as %s. kijk machine list shows the saved ones", name)
}

func machineAdd(target string, in io.Reader, out io.Writer) error {
	if err := checkTarget(target); err != nil {
		return err
	}
	if _, err := findSaved(target); err == nil {
		return fmt.Errorf("%s is already saved", target)
	}
	if err := sameLogin(target); err != nil {
		return err
	}
	s := newSSHClient(target)
	fmt.Fprintf(out, "Testing the login to %s…\n", target)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err := s.login(ctx)
	cancel()
	if err != nil {
		fmt.Fprintf(out, "kijk cannot log in to %s without a prompt: %v\n", target, err)
		if !ask(in, out, "Log in now in this terminal? [Y/n] ") {
			return loginHint(target)
		}
		if err := s.loginInTerminal(); err != nil {
			fmt.Fprintf(out, "The login failed: %v\n", err)
			return loginHint(target)
		}
	}
	err = changeMachines(func(list []savedMachine) ([]savedMachine, error) {
		if slices.ContainsFunc(list, func(m savedMachine) bool { return m.Target == target }) {
			return nil, fmt.Errorf("%s is already saved", target)
		}
		return append(list, savedMachine{target, true}), nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Saved %s. kijk installs its own copy there when needed, and an open kijk window shows its agents within a few seconds.\n", target)
	return nil
}

// sameLogin refuses a target that ssh resolves to the user, host, port and jump host of a saved
// machine, whose agents would show twice. When ssh -G fails, the login test reports why.
func sameLogin(target string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	want, err := newSSHClient(target).resolve(ctx)
	if err != nil {
		return nil
	}
	saved, err := savedMachines()
	if err != nil {
		return err
	}
	for _, m := range saved {
		if got, err := newSSHClient(m.Target).resolve(ctx); err == nil && got == want {
			return fmt.Errorf("%s is the same login as %s, which is already saved (user %s, host %s, port %s)", target, m.Target, want.user, want.host, want.port)
		}
	}
	return nil
}

func loginHint(target string) error {
	return fmt.Errorf("nothing is saved. Check the login with: ssh %s\nFor a key with a passphrase, add it to ssh-agent first: ssh-add", target)
}

// ask reads a yes or no from the terminal. Enter means yes; no input means no.
func ask(in io.Reader, out io.Writer, question string) bool {
	fmt.Fprint(out, question)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(out)
		return false
	}
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "" || a == "y" || a == "yes"
}

func machineLogin(name string, out io.Writer) error {
	if _, err := findSaved(name); err != nil {
		return err
	}
	s := newSSHClient(name)
	if s.loggedIn(context.Background()) {
		fmt.Fprintf(out, "kijk is already logged in to %s.\n", name)
		return nil
	}
	if err := s.loginInTerminal(); err != nil {
		return fmt.Errorf("could not log in to %s: %v", name, err)
	}
	fmt.Fprintf(out, "Logged in to %s. A running kijk connects within a few seconds.\n", name)
	return nil
}

func machineList(out io.Writer) error {
	list, err := savedMachines()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(out, "No machines are saved. Add one with: kijk machine add <ssh-target>")
		return nil
	}
	live, running := liveMachines()
	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	for _, m := range list {
		state, detail := "enabled", ""
		if !m.Enabled {
			state = stateDisabled
		} else if v, ok := live[m.Target]; ok {
			state, detail = v.State, v.System
			if v.State != stateConnected && v.Message != "" {
				detail = v.Message
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", m.Target, strings.ReplaceAll(state, "-", " "), detail)
	}
	w.Flush()
	if !running {
		fmt.Fprintln(out, "kijk is not running, so the connection states are not known.")
	}
	return nil
}

// liveMachines reads the machine states from the first message of a running kijk's agent list.
func liveMachines() (map[string]machineView, bool) {
	for _, port := range runningPorts() {
		s, ok := runningInstance(port)
		if !ok {
			continue
		}
		req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/api/events", port), nil)
		req.AddCookie(&http.Cookie{Name: cookieName(port), Value: s.Token})
		resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
		if err != nil {
			continue
		}
		rd := bufio.NewReaderSize(resp.Body, 64<<10)
		var msg struct {
			Machines []machineView `json:"machines"`
		}
		for {
			line, err := rd.ReadString('\n')
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				json.Unmarshal([]byte(data), &msg)
				break
			}
			if err != nil {
				break
			}
		}
		resp.Body.Close()
		live := map[string]machineView{}
		for _, m := range msg.Machines {
			live[m.Name] = m
		}
		return live, true
	}
	return nil, false
}

func machineRemove(name string, out io.Writer) error {
	err := changeMachines(func(list []savedMachine) ([]savedMachine, error) {
		i := slices.IndexFunc(list, func(m savedMachine) bool { return m.Target == name })
		if i < 0 {
			return nil, fmt.Errorf("no machine is saved as %s", name)
		}
		return slices.Delete(list, i, i+1), nil
	})
	if err != nil {
		return err
	}
	newSSHClient(name).logout()
	fmt.Fprintf(out, "Removed %s. Nothing was changed on %s.\n", name, name)
	return nil
}

func machineEnable(name string, on bool, out io.Writer) error {
	err := changeMachines(func(list []savedMachine) ([]savedMachine, error) {
		i := slices.IndexFunc(list, func(m savedMachine) bool { return m.Target == name })
		if i < 0 {
			return nil, fmt.Errorf("no machine is saved as %s", name)
		}
		list[i].Enabled = on
		return list, nil
	})
	if err != nil {
		return err
	}
	if on {
		fmt.Fprintf(out, "Enabled %s. A running kijk connects to it now.\n", name)
		return nil
	}
	newSSHClient(name).logout()
	fmt.Fprintf(out, "Disabled %s. kijk makes no SSH connection to it until you run: kijk machine enable %s\n", name, name)
	return nil
}
