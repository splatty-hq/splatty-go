package splatty

import (
	"os"
	"runtime"
	"strings"
)

// Frame is one entry of a stack trace.
type Frame struct {
	Filename string `json:"filename"`
	AbsPath  string `json:"abs_path"`
	Function string `json:"function"`
	Lineno   int    `json:"lineno"`
	InApp    bool   `json:"in_app"`
}

const maxStackDepth = 64

var workingDir = func() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}()

// captureStack walks the caller's stack, skipping the SDK's own frames.
// Frames come back oldest-first, matching the Ruby and JS clients.
func captureStack(skip int) []Frame {
	pcs := make([]uintptr, maxStackDepth)
	n := runtime.Callers(skip+2, pcs)
	if n == 0 {
		return nil
	}

	frames := runtime.CallersFrames(pcs[:n])
	out := make([]Frame, 0, n)
	for {
		frame, more := frames.Next()
		if frame.Function != "" || frame.File != "" {
			out = append(out, newFrame(frame))
		}
		if !more {
			break
		}
	}

	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func newFrame(frame runtime.Frame) Frame {
	return Frame{
		Filename: shortFilename(frame.File),
		AbsPath:  frame.File,
		Function: frame.Function,
		Lineno:   frame.Line,
		InApp:    inApp(frame.Function, frame.File),
	}
}

// shortFilename trims the noise that differs between build machines: the
// module cache prefix, the working directory, or a container's /app root.
func shortFilename(file string) string {
	if file == "" {
		return file
	}
	if i := strings.Index(file, "/pkg/mod/"); i >= 0 {
		return file[i+len("/pkg/mod/"):]
	}
	if workingDir != "" && strings.HasPrefix(file, workingDir+"/") {
		return file[len(workingDir)+1:]
	}
	if strings.HasPrefix(file, "/app/") {
		return file[len("/app/"):]
	}
	return file
}

// inApp marks frames that belong to the application rather than to the
// standard library or a dependency.
func inApp(function, file string) bool {
	if function == "" && file == "" {
		return false
	}
	if strings.Contains(file, "/pkg/mod/") {
		return false
	}
	return !isStdlib(function)
}

// isStdlib reports whether a fully-qualified function name belongs to the
// standard library. Stdlib import paths have no dot in their first segment,
// which is what separates "net/http" from "github.com/k0va1/app".
func isStdlib(function string) bool {
	if function == "" {
		return false
	}
	slash := strings.LastIndex(function, "/")
	dot := strings.Index(function[slash+1:], ".")
	if dot < 0 {
		return true
	}
	pkg := function[:slash+1+dot]
	first := pkg
	if i := strings.Index(pkg, "/"); i >= 0 {
		first = pkg[:i]
	}
	return !strings.Contains(first, ".")
}
