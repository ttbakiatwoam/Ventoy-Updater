package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

type Logger struct {
	out  io.Writer
	json bool
	mu   sync.Mutex
}

type Fields map[string]any

func New(out io.Writer, jsonOutput bool) *Logger {
	return &Logger{out: out, json: jsonOutput}
}

func (l *Logger) Info(message string, fields Fields) {
	l.log("info", message, fields)
}

func (l *Logger) Warn(message string, fields Fields) {
	l.log("warn", message, fields)
}

func (l *Logger) Error(message string, fields Fields) {
	l.log("error", message, fields)
}

func (l *Logger) log(level, message string, fields Fields) {
	if l == nil || l.out == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if fields == nil {
		fields = Fields{}
	}
	if l.json {
		record := map[string]any{
			"time":    time.Now().UTC().Format(time.RFC3339),
			"level":   level,
			"message": message,
		}
		for k, v := range fields {
			record[k] = v
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			fmt.Fprintf(l.out, `{"level":"error","message":"failed to encode log record"}`+"\n")
			return
		}
		fmt.Fprintln(l.out, string(encoded))
		return
	}

	var parts []string
	for k, v := range fields {
		parts = append(parts, fmt.Sprintf("%s=%s", sanitize(k), quoteValue(v)))
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		fmt.Fprintf(l.out, "%s %s\n", strings.ToUpper(level), message)
		return
	}
	fmt.Fprintf(l.out, "%s %s %s\n", strings.ToUpper(level), message, strings.Join(parts, " "))
}

func sanitize(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, " ", "_")
	if value == "" {
		return "field"
	}
	return value
}

func quoteValue(value any) string {
	s := fmt.Sprint(value)
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t\n\"") {
		return fmt.Sprintf("%q", s)
	}
	return s
}
