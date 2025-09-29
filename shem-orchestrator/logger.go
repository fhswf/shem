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
