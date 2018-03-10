// up is the ultimate pipe composer/editor. It helps building Linux pipelines
// in a terminal-based UI interactively, with live preview of command results.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"unicode/utf8"

	"github.com/mattn/go-isatty"
	termbox "github.com/nsf/termbox-go"
)

func main() {
	// TODO: Without below block, we'd hang with no piped input (see github.com/peco/peco, mattn/gof, fzf, etc.)
	if isatty.IsTerminal(os.Stdin.Fd()) {
		fmt.Fprintln(os.Stderr, "error: up requires some data piped on standard input, e.g.: `echo hello world | up`")
		os.Exit(1)
	}

	// Init TUI code
	// TODO: maybe try gocui or tcell?
	err := termbox.Init()
	if err != nil {
		panic(err)
	}
	defer termbox.Close()

	var (
		editor      = NewEditor("| ")
		lastCommand = ""
		subprocess  *Subprocess
		inputBuf    = NewBuf()
		buf         = inputBuf
	)

	// In background, start collecting input from stdin to internal buffer of size 40 MB, then pause it
	go inputBuf.Collect(os.Stdin)

	// Main loop
main_loop:
	for {
		// Run command in background if needed
		command := editor.String()
		if command != lastCommand {
			lastCommand = command
			subprocess.Kill()
			subprocess = StartSubprocess(inputBuf, command)
			buf = subprocess.Buf
		}

		// Draw command input line
		editor.Draw(0, 0, true)
		buf.Draw(1)
		termbox.Flush()

		// Handle events
		// TODO: how to interject with timer events triggering refresh?
		switch ev := termbox.PollEvent(); ev.Type {
		case termbox.EventKey:
			// handle command-line editing keys
			if editor.HandleKey(ev) {
				continue main_loop
			}
			// handle other keys
			switch ev.Key {
			case termbox.KeyEsc, termbox.KeyCtrlC:
				// quit
				return
			}
		}
	}

	// TODO: run command automatically in bg after first " " (or ^Enter), via `bash -c`
	// TODO: auto-kill the child process on any edit
	// TODO: allow scrolling the output preview with pgup/pgdn keys
	// TODO: [LATER] Ctrl-O shows input via `less` or $PAGER
	// TODO: ^X - save into executable file upN.sh (with #!/bin/bash) and quit
	// TODO: properly show all licenses of dependencies on --version
	// TODO: [LATER] allow increasing size of input buffer with some key
	// TODO: [LATER] on ^X, leave TUI and run the command through buffered input, then unpause rest of input
	// TODO: [LATER] allow adding more elements of pipeline (initially, just writing `foo | bar` should work)
	// TODO: [LATER] allow invocation with partial command, like: `up grep -i`
	// TODO: [LATER][MAYBE] allow reading upN.sh scripts
	// TODO: [LATER] auto-save and/or save on Ctrl-S or something
	// TODO: [MUCH LATER] readline-like rich editing support? and completion?
	// TODO: [MUCH LATER] integration with fzf? and pindexis/marker?
	// TODO: [LATER] forking and unforking pipelines
	// TODO: [LATER] capture output of a running process (see: https://stackoverflow.com/q/19584825/98528)
	// TODO: [LATER] richer TUI:
	// - show # of read lines & kbytes
	// - show status (errorlevel) of process, or that it's still running (also with background colors)
	// - allow copying and pasting to/from command line
	// TODO: [LATER] allow connecting external editor (become server/engine via e.g. socket)
	// TODO: [LATER] become pluggable into http://luna-lang.org
	// TODO: [LATER][MAYBE] allow "plugins" ("combos" - commands with default options) e.g. for Lua `lua -e`+auto-quote, etc.
	// TODO: [LATER] make it more friendly to infrequent Linux users by providing "descriptive" commands like "search" etc.
	// TODO: [LATER] advertise on: HN, r/programming, r/golang, r/commandline, r/linux; data exploration? data science?
}

type Buf struct {
	bytes []byte
	// NOTE: n can be written only by Collect
	n     int
	nLock sync.Mutex
}

func NewBuf() *Buf {
	const bufsize = 40 * 1024 * 1024 // 40 MB
	return &Buf{bytes: make([]byte, bufsize)}
}

func (b *Buf) Collect(r io.Reader) {
	// TODO: allow stopping - take context?
	for {
		n, err := r.Read(b.bytes[b.n:])
		b.nLock.Lock()
		b.n += n
		b.nLock.Unlock()
		go termbox.Interrupt()
		if err == io.EOF {
			// TODO: mark work as complete
			return
		} else if err != nil {
			// TODO: better handling of errors
			panic(err)
		}
		if b.n == len(b.bytes) {
			return
		}
	}
}

func (b *Buf) Draw(y0 int) {
	b.nLock.Lock()
	buf := b.bytes[:b.n]
	b.nLock.Unlock()
	w, h := termbox.Size()
	// TODO: handle runes properly, including their visual width (mattn/go-runewidth)
	x, y := 0, y0
	for len(buf) > 0 && y < h {
		ch, sz := utf8.DecodeRune(buf)
		buf = buf[sz:]
		switch ch {
		case '\n':
			// TODO: clear to the end of screen line
			x, y = 0, y+1
			continue
		case '\t':
			const tabwidth = 8
			b.putch(x, y, ' ')
			for x%tabwidth < (tabwidth - 1) {
				x++
				if x >= w {
					break
				}
				b.putch(x, y, ' ')
			}
		default:
			b.putch(x, y, ch)
		}
		x++
		if x > w {
			x, y = 0, y+1
		}
	}
}

func (b *Buf) putch(x, y int, ch rune) {
	termbox.SetCell(x, y, ch, termbox.ColorDefault, termbox.ColorDefault)
}

func (b *Buf) NewReader() io.Reader {
	// TODO: return EOF if input is fully buffered?
	i := 0
	return funcReader(func(p []byte) (n int, err error) {
		b.nLock.Lock()
		end := b.n
		b.nLock.Unlock()
		// TODO: don't return (0,nil), instead wait until at least 1 available,
		// or return EOF on completion?
		n = copy(p, b.bytes[i:end])
		i += n
		return n, nil
	})
}

type funcReader func([]byte) (int, error)

func (f funcReader) Read(p []byte) (int, error) { return f(p) }

type Editor struct {
	prompt []rune
	// TODO: make editor multiline. Reuse gocui or something for this?
	// TODO: rename 'command' to 'data' or 'value' or something more generic
	command []rune
	cursor  int
	// lastw is length of command on last Draw
	lastw int
}

func NewEditor(prompt string) *Editor {
	return &Editor{prompt: []rune(prompt)}
}

func (e *Editor) String() string {
	return string(e.command)
}

func (e *Editor) Draw(x, y int, setcursor bool) {
	for i, ch := range e.prompt {
		termbox.SetCell(x+i, y, ch, termbox.ColorWhite, termbox.ColorBlue)
	}
	for i, ch := range e.command {
		termbox.SetCell(x+len(e.prompt)+i, y, ch, termbox.ColorWhite, termbox.ColorBlue)
	}
	// clear remains of last command if needed
	for i := len(e.command); i < e.lastw; i++ {
		termbox.SetCell(x+len(e.prompt)+i, y, ' ', termbox.ColorDefault, termbox.ColorDefault)
	}
	if setcursor {
		termbox.SetCursor(x+len(e.prompt)+e.cursor, y)
	}
	e.lastw = len(e.command)
}

func (e *Editor) HandleKey(ev termbox.Event) bool {
	if ev.Type != termbox.EventKey {
		return false
	}
	if ev.Ch != 0 {
		e.insert(ev.Ch)
		return true
	}
	switch ev.Key {
	case termbox.KeySpace:
		e.insert(' ')
	case termbox.KeyBackspace, termbox.KeyBackspace2:
		// See https://github.com/nsf/termbox-go/issues/145
		e.delete(-1)
	case termbox.KeyDelete:
		e.delete(0)
	case termbox.KeyArrowLeft:
		if e.cursor > 0 {
			e.cursor--
		}
	case termbox.KeyArrowRight:
		if e.cursor < len(e.command) {
			e.cursor++
		}
	default:
		return false
	}
	return true
}

func (e *Editor) insert(ch rune) {
	// insert key into command (https://github.com/golang/go/wiki/SliceTricks#insert)
	e.command = append(e.command, 0)
	copy(e.command[e.cursor+1:], e.command[e.cursor:])
	e.command[e.cursor] = ch
	e.cursor++
}

func (e *Editor) delete(dx int) {
	pos := e.cursor + dx
	if pos < 0 || pos >= len(e.command) {
		return
	}
	e.command = append(e.command[:pos], e.command[pos+1:]...)
	e.cursor = pos
}

type Subprocess struct {
	Buf    *Buf
	cancel context.CancelFunc
}

func StartSubprocess(inputBuf *Buf, command string) *Subprocess {
	ctx, cancel := context.WithCancel(context.TODO())
	s := &Subprocess{
		Buf:    NewBuf(),
		cancel: cancel,
	}
	r, w := io.Pipe()
	go s.Buf.Collect(r)

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Stdout = w
	cmd.Stderr = w
	cmd.Stdin = inputBuf.NewReader()
	err := cmd.Start()
	if err != nil {
		fmt.Fprintf(w, "up: %s", err)
		return s
	}
	go cmd.Wait()
	return s
}

func (s *Subprocess) Kill() {
	if s == nil {
		return
	}
	s.cancel()
}
