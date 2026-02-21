package main

import (
	"fmt"
	"os"
)

// Very simple logger that depends on systemd to add a timestamp and interpret the log level
type Logger struct {
	component string
}

func NewLogger(component string) *Logger {
	return &Logger{
		component: component,
	}
}

func (l *Logger) Debug(format string, args ...any) {
	fmt.Fprintf(os.Stdout, "<7>[%s] %s\n", l.component, fmt.Sprintf(format, args...))
}

func (l *Logger) Info(format string, args ...any) {
	fmt.Fprintf(os.Stdout, "<6>[%s] %s\n", l.component, fmt.Sprintf(format, args...))
}

func (l *Logger) Warn(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "<4>[%s] %s\n", l.component, fmt.Sprintf(format, args...))
}

func (l *Logger) Error(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "<3>[%s] %s\n", l.component, fmt.Sprintf(format, args...))
}

// Log does not add a log level, but keeps it if it is provided in its arguments
func (l *Logger) Log(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if len(msg) >= 3 && msg[0] == '<' && msg[1] >= '0' && msg[1] <= '7' && msg[2] == '>' {
		fmt.Fprintf(os.Stderr, "%s[%s] %s\n", msg[:3], l.component, msg[3:])
	} else {
		fmt.Fprintf(os.Stderr, "[%s] %s\n", l.component, msg)
	}
}
