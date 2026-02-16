// Message validation, parsing, and encoding for SHEM module communication.
// See modules.md for a description of the message format.

package shemmsg

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const (
	MaxNameLength   = 100
	MaxMessageBytes = 10000
	TimeStepMinutes = 5
)

var (
	ErrInvalidName       = errors.New("invalid variable name")
	ErrInvalidValue      = errors.New("invalid numeric value")
	ErrValueOutOfRange   = errors.New("value outside allowed range")
	ErrInvalidTimestamp  = errors.New("invalid or misaligned timestamp")
	ErrUnknownType       = errors.New("unknown message type")
	ErrMessageTooLarge   = errors.New("message exceeds maximum size")
	ErrEmptyMessage      = errors.New("empty message")
	ErrMissingValue      = errors.New("pointvalue requires exactly one value line")
	ErrMissingTimestamp  = errors.New("timeseries requires timestamp and at least one value")
	ErrInvalidCharacters = errors.New("message contains invalid characters")
)

// Value represents a numeric value that may be missing.
type Value struct {
	value   float64
	missing bool
}

// Missing returns a Value representing a missing measurement.
func Missing() Value {
	return Value{missing: true}
}

// Number creates a Value from a float64. Its validity is checked by encoding it and then
// validating the encoded value. Too large numbers, NaN, Inf etc. are rejected with an error.
func Number(f float64) (Value, error) {
	v := Value{value: f, missing: false}
	if !isValidNumberFormat(v.String()) {
		return Missing(), ErrValueOutOfRange
	}
	return v, nil
}

// IsMissing reports whether the value is missing.
func (v Value) IsMissing() bool {
	return v.missing
}

// Float64 returns the numeric value.
// Panics if the value is missing; check IsMissing() first.
func (v Value) Float64() float64 {
	if v.missing {
		panic("Float64 called on missing value")
	}
	return v.value
}

// String returns the string representation of the value. Numeric values are always formatted with
// 3 decimal digits.
func (v Value) String() string {
	if v.missing {
		return "missing"
	}
	return strconv.FormatFloat(v.value, 'f', 3, 64)
}

func parseValue(s string) (Value, error) {
	s = strings.TrimSpace(s)

	if s == "missing" {
		return Missing(), nil
	}

	if !isValidNumberFormat(s) {
		return Missing(), ErrInvalidValue
	}

	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return Missing(), ErrInvalidValue
	}

	return Value{value: f, missing: false}, nil
}

// isValidNumberFormat checks that the string matches the expected format:
// optional sign, up to 8 digits before the decimal point, optional decimal
// point with up to 3 digits after it.
func isValidNumberFormat(s string) bool {
	if len(s) == 0 {
		return false
	}

	i := 0

	// Optional sign
	if s[i] == '+' || s[i] == '-' {
		i++
		if i >= len(s) {
			return false
		}
	}

	// Count digits before decimal point
	digitsBefore := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		digitsBefore++
		i++
	}

	// Optional decimal point and fractional digits
	digitsAfter := 0
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			digitsAfter++
			i++
		}
	}

	// Must have consumed the entire string
	if i != len(s) {
		return false
	}

	// Must have at least one digit somewhere
	if digitsBefore+digitsAfter == 0 {
		return false
	}

	// Enforce digit count limits
	if digitsBefore > 8 || digitsAfter > 3 {
		return false
	}

	return true
}

// ParseError includes the line that could not be parsed.
type ParseError struct {
	Message string
	Content string // the offending line
}

func (e *ParseError) Error() string {
	content := e.Content
	if len(content) > 100 {
		content = content[:97] + "..."
	}
	return fmt.Sprintf("%s: %q", e.Message, content)
}

// Message represents a parsed message with a name and payload.
type Message struct {
	Name    string
	Payload Payload
}

// Payload is implemented by all payload types.
type Payload interface {
	payloadType() string
	encodePayload() []byte
}

// Type returns the message type identifier ("pointvalue" or "timeseries").
func (m Message) Type() string {
	return m.Payload.payloadType()
}

// WithName returns a copy of the message with a different name.
func (m Message) WithName(name string) Message {
	return Message{Name: name, Payload: m.Payload}
}

// Encode returns the message in canonical format (without surrounding newlines).
func (m Message) Encode() []byte {
	var buf bytes.Buffer
	buf.WriteString(m.Payload.payloadType())
	buf.WriteByte(' ')
	buf.WriteString(m.Name)
	buf.WriteByte('\n')
	buf.Write(m.Payload.encodePayload())
	return buf.Bytes()
}

// PointValue is a Payload that represents a single measurement at the current time.
type PointValue struct {
	Value Value
}

func (p PointValue) payloadType() string {
	return "pointvalue"
}

func (p PointValue) encodePayload() []byte {
	return []byte(p.Value.String())
}

// TimeSeries represents a sequence of values at 5-minute intervals.
type TimeSeries struct {
	StartTime time.Time // must be aligned to 5-minute boundary, UTC
	Values    []Value
}

func (t TimeSeries) payloadType() string {
	return "timeseries"
}

func (t TimeSeries) encodePayload() []byte {
	var buf bytes.Buffer
	buf.WriteString(t.StartTime.UTC().Format("2006-01-02T15:04"))
	for _, v := range t.Values {
		buf.WriteByte('\n')
		buf.WriteString(v.String())
	}
	return buf.Bytes()
}

// Parse parses a single message. The input should not include the surrounding blank lines.
func Parse(data []byte) (Message, error) {
	if len(data) > MaxMessageBytes {
		return Message{}, ErrMessageTooLarge
	}

	if !isPrintableASCII(data) {
		return Message{}, ErrInvalidCharacters
	}

	text := string(data)
	lines := strings.Split(text, "\n")

	// Remove trailing empty lines
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if len(lines) == 0 {
		return Message{}, ErrEmptyMessage
	}

	// Parse header line: "type name"
	header := strings.Fields(lines[0])
	if len(header) != 2 {
		return Message{}, &ParseError{Content: lines[0], Message: "expected 'type name'"}
	}

	msgType := header[0]
	name := header[1]

	if err := ValidateName(name); err != nil {
		return Message{}, &ParseError{Content: lines[0], Message: err.Error()}
	}

	var payload Payload
	var err error

	switch msgType {
	case "pointvalue":
		payload, err = parsePointValue(lines[1:])
	case "timeseries":
		payload, err = parseTimeSeries(lines[1:])
	default:
		return Message{}, &ParseError{Content: lines[0], Message: ErrUnknownType.Error()}
	}

	if err != nil {
		return Message{}, err
	}

	return Message{Name: name, Payload: payload}, nil
}

// isPrintableASCII checks if all bytes are printable ASCII (0x20-0x7E) or newline (0x0A).
func isPrintableASCII(data []byte) bool {
	for _, b := range data {
		if b == '\n' || (b >= 0x20 && b <= 0x7E) {
			continue
		}
		return false
	}
	return true
}

// SplitName splits "module.variable" into components. It does not validate the name.
// Returns ("", name) if there's no dot (unqualified name).
func SplitName(name string) (module, variable string) {
	idx := strings.Index(name, ".")
	if idx < 0 {
		return "", name
	}

	module = name[:idx]
	variable = name[idx+1:]

	return module, variable
}

// ValidateNamePart checks if a name component (module or variable) is valid.
// Names must be 1-100 characters, alphanumeric plus underscore.
func ValidateNamePart(name string) error {
	if len(name) == 0 {
		return fmt.Errorf("%w: empty name", ErrInvalidName)
	}

	if len(name) > MaxNameLength {
		return fmt.Errorf("%w: exceeds %d characters", ErrInvalidName, MaxNameLength)
	}

	for _, c := range name {
		if !isNameChar(c) {
			return fmt.Errorf("%w: invalid character '%c'", ErrInvalidName, c)
		}
	}

	return nil
}

// ValidateName checks names and allows for qualified names (module.variable).
func ValidateName(name string) error {
	// Check for leading dot (SplitName returns empty module for ".foo")
	if len(name) > 0 && name[0] == '.' {
		return fmt.Errorf("%w: dot at start", ErrInvalidName)
	}

	module, variable := SplitName(name)

	if err := ValidateNamePart(variable); err != nil {
		return err
	}
	if module == "" {
		return nil
	}
	return ValidateNamePart(module)
}

func isNameChar(c rune) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '_'
}

func parsePointValue(lines []string) (PointValue, error) {
	if len(lines) != 1 {
		return PointValue{}, ErrMissingValue
	}

	val, err := parseValue(lines[0])
	if err != nil {
		return PointValue{}, &ParseError{Message: err.Error(), Content: lines[0]}
	}

	return PointValue{Value: val}, nil
}

func parseTimeSeries(lines []string) (TimeSeries, error) {
	if len(lines) < 2 {
		return TimeSeries{}, ErrMissingTimestamp
	}

	// Parse timestamp
	ts, err := time.Parse("2006-01-02T15:04", lines[0])
	if err != nil {
		return TimeSeries{}, &ParseError{Content: lines[0], Message: ErrInvalidTimestamp.Error()}
	}

	// Verify 5-minute alignment
	if ts.Minute()%TimeStepMinutes != 0 {
		return TimeSeries{}, &ParseError{Content: lines[0], Message: "timestamp must be aligned to 5-minute boundary"}
	}

	// Parse values
	values := make([]Value, 0, len(lines)-1)
	for _, line := range lines[1:] {
		val, err := parseValue(line)
		if err != nil {
			return TimeSeries{}, &ParseError{Message: err.Error(), Content: line}
		}
		values = append(values, val)
	}

	return TimeSeries{StartTime: ts, Values: values}, nil
}

// Reader reads messages from a stream, handling the double-newline separation.
type Reader struct {
	scanner *bufio.Scanner
	buf     bytes.Buffer
}

// scanNewlines is a split function that splits on \n only, unlike bufio.ScanLines
// which also strips \r.
func scanNewlines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[0:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// NewReader creates a Reader that reads messages from r.
func NewReader(r io.Reader) *Reader {
	scanner := bufio.NewScanner(r)
	scanner.Split(scanNewlines)
	return &Reader{scanner: scanner}
}

// Read returns the next message from the stream.
// Returns io.EOF when the stream is closed cleanly.
func (r *Reader) Read() (Message, error) {
	r.buf.Reset()

	// Skip leading empty lines
	for r.scanner.Scan() {
		line := r.scanner.Text()
		if line != "" {
			r.buf.WriteString(line)
			r.buf.WriteByte('\n')
			break
		}
	}

	if err := r.scanner.Err(); err != nil {
		return Message{}, err
	}

	// If we got nothing, we've reached EOF
	if r.buf.Len() == 0 {
		return Message{}, io.EOF
	}

	// Read until empty line or EOF
	for r.scanner.Scan() {
		line := r.scanner.Text()
		if line == "" {
			break
		}
		r.buf.WriteString(line)
		r.buf.WriteByte('\n')

		if r.buf.Len() > MaxMessageBytes {
			return Message{}, ErrMessageTooLarge
		}
	}

	if err := r.scanner.Err(); err != nil {
		return Message{}, err
	}

	return Parse(r.buf.Bytes())
}

// Writer writes messages to a stream with proper separation.
type Writer struct {
	w io.Writer
}

// NewWriter creates a Writer that writes messages to w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// Write encodes and writes a message with surrounding newlines.
func (w *Writer) Write(m Message) error {
	var buf bytes.Buffer
	buf.WriteByte('\n')
	buf.WriteByte('\n')
	buf.Write(m.Encode())
	buf.WriteByte('\n')
	buf.WriteByte('\n')

	_, err := w.w.Write(buf.Bytes())
	return err
}
