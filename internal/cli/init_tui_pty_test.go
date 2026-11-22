/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// openPty returns a pty master and its slave; the slave keeps the kernel defaults (ONLCR on).
func openPty(t *testing.T) (master, slave *os.File) {
	t.Helper()

	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no /dev/ptmx: %v", err)
	}

	if err := unix.IoctlSetPointerInt(int(m.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}

	n, err := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}

	s, err := os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = m.Close(); _ = s.Close() })

	return m, s
}

// TestAskPrompts_InputAndOutputOnDifferentTerminals: bubbletea raws the input terminal and then draws
// with bare line feeds, assuming the output does not translate them. With the output a different
// terminal that still has ONLCR, each bare line feed would return the carriage and the cursor
// moves after it would land in the wrong column. The output terminal must see the bare line feeds
// and get its settings back afterwards.
func TestAskPrompts_InputAndOutputOnDifferentTerminals(t *testing.T) {
	setTerminal(t, true)

	inM, inS := openPty(t)
	outM, outS := openPty(t)

	got := make(chan []byte, 1)

	go func() { b, _ := io.ReadAll(outM); got <- b }()

	go func() {
		time.Sleep(700 * time.Millisecond)
		_, _ = inM.Write([]byte("\x1b[B"))
		time.Sleep(300 * time.Millisecond)
		_, _ = inM.Write([]byte("\x03"))
	}()

	qs := []promptQuestion{{flag: flagInstallDisk, label: "Install disk", choices: diskChoices(testDisks), noDefault: true}}
	if _, err := askPrompts(qs, nil, inS, outS); !errors.Is(err, errPromptCancelled) {
		t.Fatalf("err = %v", err)
	}

	tc, err := unix.IoctlGetTermios(int(outS.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}

	if tc.Oflag&unix.ONLCR == 0 {
		t.Error("the output terminal's ONLCR was not restored")
	}

	_ = outS.Close()

	var seen []byte

	select {
	case seen = <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("the output terminal never closed")
	}

	if bare := bytes.Count(seen, []byte("\n")) - bytes.Count(seen, []byte("\r\n")); bare == 0 {
		t.Errorf("every line feed was translated to CR LF, so the cursor moves after it are off: %q", seen)
	}
}
