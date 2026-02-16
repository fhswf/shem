package shemmsg

import (
	"bytes"
	"io"
	"math"
	"strings"
	"testing"
	"time"
)

func TestValue(t *testing.T) {
	t.Run("Number valid", func(t *testing.T) {
		v, err := Number(123.45)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v.IsMissing() {
			t.Fatal("expected non-missing value")
		}
		if v.Float64() != 123.45 {
			t.Fatalf("expected 123.45, got %v", v.Float64())
		}
	})

	t.Run("Number zero", func(t *testing.T) {
		v, err := Number(0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v.Float64() != 0 {
			t.Fatalf("expected 0, got %v", v.Float64())
		}
	})

	t.Run("Number negative", func(t *testing.T) {
		v, err := Number(-802.10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v.Float64() != -802.10 {
			t.Fatalf("expected -802.10, got %v", v.Float64())
		}
	})

	t.Run("Number max valid", func(t *testing.T) {
		v, err := Number(99999999.999)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v.String() != "99999999.999" {
			t.Fatalf("expected 99999999.999, got %v", v.String())
		}
	})

	t.Run("Number out of range positive", func(t *testing.T) {
		_, err := Number(100_000_000)
		if err != ErrValueOutOfRange {
			t.Fatalf("expected ErrValueOutOfRange, got %v", err)
		}
	})

	t.Run("Number out of range negative", func(t *testing.T) {
		_, err := Number(-100_000_000)
		if err != ErrValueOutOfRange {
			t.Fatalf("expected ErrValueOutOfRange, got %v", err)
		}
	})

	t.Run("Number NaN rejected", func(t *testing.T) {
		_, err := Number(math.NaN())
		if err != ErrValueOutOfRange {
			t.Fatalf("expected ErrValueOutOfRange, got %v", err)
		}
	})

	t.Run("Number Inf rejected", func(t *testing.T) {
		_, err := Number(math.Inf(1))
		if err != ErrValueOutOfRange {
			t.Fatalf("expected ErrValueOutOfRange, got %v", err)
		}
	})

	t.Run("Missing", func(t *testing.T) {
		v := Missing()
		if !v.IsMissing() {
			t.Fatal("expected missing value")
		}
	})

	t.Run("Missing Float64 panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic")
			}
		}()
		v := Missing()
		_ = v.Float64()
	})
}

func TestValueEncode(t *testing.T) {
	tests := []struct {
		name     string
		value    Value
		expected string
	}{
		{"positive integer", mustNumber(123), "123.000"},
		{"negative integer", mustNumber(-456), "-456.000"},
		{"zero", mustNumber(0), "0.000"},
		{"decimal", mustNumber(123.456), "123.456"},
		{"negative decimal", mustNumber(-802.10), "-802.100"},
		{"small decimal", mustNumber(0.5), "0.500"},
		{"missing", Missing(), "missing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.value.String()
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestParsePointValue(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		check   func(t *testing.T, m Message)
	}{
		{
			name:  "simple positive",
			input: "pointvalue net_power\n123.45",
			check: func(t *testing.T, m Message) {
				if m.Name != "net_power" {
					t.Errorf("expected name 'net_power', got %q", m.Name)
				}
				pv, ok := m.Payload.(PointValue)
				if !ok {
					t.Fatal("expected PointValue payload")
				}
				if pv.Value.Float64() != 123.45 {
					t.Errorf("expected 123.45, got %v", pv.Value.Float64())
				}
			},
		},
		{
			name:  "negative value",
			input: "pointvalue power\n-802.10",
			check: func(t *testing.T, m Message) {
				pv := m.Payload.(PointValue)
				if pv.Value.Float64() != -802.10 {
					t.Errorf("expected -802.10, got %v", pv.Value.Float64())
				}
			},
		},
		{
			name:  "missing value",
			input: "pointvalue irradiance\nmissing",
			check: func(t *testing.T, m Message) {
				pv := m.Payload.(PointValue)
				if !pv.Value.IsMissing() {
					t.Error("expected missing value")
				}
			},
		},
		{
			name:  "integer value",
			input: "pointvalue total_energy\n9371802",
			check: func(t *testing.T, m Message) {
				pv := m.Payload.(PointValue)
				if pv.Value.Float64() != 9371802 {
					t.Errorf("expected 9371802, got %v", pv.Value.Float64())
				}
			},
		},
		{
			name:  "qualified name",
			input: "pointvalue meter.net_power\n100",
			check: func(t *testing.T, m Message) {
				if m.Name != "meter.net_power" {
					t.Errorf("expected name 'meter.net_power', got %q", m.Name)
				}
			},
		},
		{
			name:    "invalid type",
			input:   "badtype foo\n123",
			wantErr: true,
		},
		{
			name:    "missing name",
			input:   "pointvalue\n123",
			wantErr: true,
		},
		{
			name:    "empty value",
			input:   "pointvalue foo\n",
			wantErr: true,
		},
		{
			name:    "scientific notation rejected",
			input:   "pointvalue foo\n1e5",
			wantErr: true,
		},
		{
			name:    "invalid name characters",
			input:   "pointvalue foo-bar\n123",
			wantErr: true,
		},
		{
			name:    "control character rejected",
			input:   "pointvalue foo\n12\x003",
			wantErr: true,
		},
		{
			name:    "high byte rejected",
			input:   "pointvalue foo\n12\x803",
			wantErr: true,
		},
		{
			name:    "tab rejected",
			input:   "pointvalue foo\n12\t3",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Parse([]byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, m)
			}
		})
	}
}

func TestParseTimeSeries(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		check   func(t *testing.T, m Message)
	}{
		{
			name:  "simple timeseries",
			input: "timeseries pv_forecast\n2025-12-06T08:00\n120.0\n145.1\n140.5",
			check: func(t *testing.T, m Message) {
				if m.Name != "pv_forecast" {
					t.Errorf("expected name 'pv_forecast', got %q", m.Name)
				}
				ts, ok := m.Payload.(TimeSeries)
				if !ok {
					t.Fatal("expected TimeSeries payload")
				}
				expected := time.Date(2025, 12, 6, 8, 0, 0, 0, time.UTC)
				if !ts.StartTime.Equal(expected) {
					t.Errorf("expected start time %v, got %v", expected, ts.StartTime)
				}
				if len(ts.Values) != 3 {
					t.Fatalf("expected 3 values, got %d", len(ts.Values))
				}
				if ts.Values[0].Float64() != 120.0 {
					t.Errorf("expected first value 120.0, got %v", ts.Values[0].Float64())
				}
			},
		},
		{
			name:  "with missing value",
			input: "timeseries forecast\n2025-12-06T08:00\n120.0\nmissing\n140.5",
			check: func(t *testing.T, m Message) {
				ts := m.Payload.(TimeSeries)
				if !ts.Values[1].IsMissing() {
					t.Error("expected second value to be missing")
				}
			},
		},
		{
			name:    "misaligned timestamp",
			input:   "timeseries foo\n2025-12-06T08:03\n120.0",
			wantErr: true,
		},
		{
			name:    "no values",
			input:   "timeseries foo\n2025-12-06T08:00",
			wantErr: true,
		},
		{
			name:    "invalid timestamp",
			input:   "timeseries foo\n2025-13-06T08:00\n120.0",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Parse([]byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, m)
			}
		})
	}
}

func TestMessageEncode(t *testing.T) {
	t.Run("pointvalue", func(t *testing.T) {
		m := Message{
			Name:    "net_power",
			Payload: PointValue{Value: mustNumber(-802.1)},
		}
		got := string(m.Encode())
		expected := "pointvalue net_power\n-802.100"
		if got != expected {
			t.Errorf("expected %q, got %q", expected, got)
		}
	})

	t.Run("timeseries", func(t *testing.T) {
		m := Message{
			Name: "pv_forecast",
			Payload: TimeSeries{
				StartTime: time.Date(2025, 12, 6, 8, 0, 0, 0, time.UTC),
				Values:    []Value{mustNumber(120), Missing(), mustNumber(140.5)},
			},
		}
		got := string(m.Encode())
		expected := "timeseries pv_forecast\n2025-12-06T08:00\n120.000\nmissing\n140.500"
		if got != expected {
			t.Errorf("expected %q, got %q", expected, got)
		}
	})
}

func TestMessageWithName(t *testing.T) {
	original := Message{
		Name:    "original_name",
		Payload: PointValue{Value: mustNumber(123)},
	}

	renamed := original.WithName("new_name")

	if renamed.Name != "new_name" {
		t.Errorf("expected name 'new_name', got %q", renamed.Name)
	}
	if original.Name != "original_name" {
		t.Error("original message was modified")
	}
}

func TestRoundTrip(t *testing.T) {
	messages := []Message{
		{
			Name:    "net_power",
			Payload: PointValue{Value: mustNumber(-802.1)},
		},
		{
			Name:    "meter.total_energy",
			Payload: PointValue{Value: mustNumber(9371802)},
		},
		{
			Name:    "sensor.reading",
			Payload: PointValue{Value: Missing()},
		},
		{
			Name: "pv_forecast",
			Payload: TimeSeries{
				StartTime: time.Date(2025, 12, 6, 8, 0, 0, 0, time.UTC),
				Values:    []Value{mustNumber(120), Missing(), mustNumber(140.5)},
			},
		},
	}

	for _, original := range messages {
		t.Run(original.Name, func(t *testing.T) {
			encoded := original.Encode()
			decoded, err := Parse(encoded)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			if decoded.Name != original.Name {
				t.Errorf("name mismatch: expected %q, got %q", original.Name, decoded.Name)
			}
			if decoded.Type() != original.Type() {
				t.Errorf("type mismatch: expected %q, got %q", original.Type(), decoded.Type())
			}

			// Re-encode and compare
			reencoded := decoded.Encode()
			if !bytes.Equal(encoded, reencoded) {
				t.Errorf("re-encoded mismatch:\noriginal:   %q\nre-encoded: %q", encoded, reencoded)
			}
		})
	}
}

func TestReaderWriter(t *testing.T) {
	var buf bytes.Buffer
	writer := NewWriter(&buf)

	messages := []Message{
		{Name: "power", Payload: PointValue{Value: mustNumber(100)}},
		{Name: "energy", Payload: PointValue{Value: mustNumber(200)}},
		{Name: "forecast", Payload: TimeSeries{
			StartTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			Values:    []Value{mustNumber(1), mustNumber(2)},
		}},
	}

	for _, m := range messages {
		if err := writer.Write(m); err != nil {
			t.Fatalf("write error: %v", err)
		}
	}

	reader := NewReader(&buf)
	for i, expected := range messages {
		got, err := reader.Read()
		if err != nil {
			t.Fatalf("read %d error: %v", i, err)
		}
		if got.Name != expected.Name {
			t.Errorf("message %d: expected name %q, got %q", i, expected.Name, got.Name)
		}
	}

	// Should get EOF
	_, err := reader.Read()
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestReaderSkipsEmptyLines(t *testing.T) {
	input := "\n\n\npointvalue foo\n123\n\n\n\npointvalue bar\n456\n\n"
	reader := NewReader(strings.NewReader(input))

	m1, err := reader.Read()
	if err != nil {
		t.Fatalf("read 1 error: %v", err)
	}
	if m1.Name != "foo" {
		t.Errorf("expected name 'foo', got %q", m1.Name)
	}

	m2, err := reader.Read()
	if err != nil {
		t.Fatalf("read 2 error: %v", err)
	}
	if m2.Name != "bar" {
		t.Errorf("expected name 'bar', got %q", m2.Name)
	}
}

func TestReaderRejectsCRLF(t *testing.T) {
	// Carriage return should be rejected, not silently stripped
	input := "pointvalue foo\r\n123\n\n"
	reader := NewReader(strings.NewReader(input))

	_, err := reader.Read()
	if err != ErrInvalidCharacters {
		t.Errorf("expected ErrInvalidCharacters, got %v", err)
	}
}

func TestNameHandling(t *testing.T) {
	t.Run("SplitName qualified", func(t *testing.T) {
		module, variable := SplitName("meter.net_power")
		if module != "meter" || variable != "net_power" {
			t.Errorf("expected ('meter', 'net_power'), got (%q, %q)", module, variable)
		}
	})

	t.Run("SplitName unqualified", func(t *testing.T) {
		module, variable := SplitName("net_power")
		if module != "" || variable != "net_power" {
			t.Errorf("expected ('', 'net_power'), got (%q, %q)", module, variable)
		}
	})

	t.Run("SplitName multiple dots", func(t *testing.T) {
		// SplitName doesn't validate; it just splits on first dot
		module, variable := SplitName("a.b.c")
		if module != "a" || variable != "b.c" {
			t.Errorf("expected ('a', 'b.c'), got (%q, %q)", module, variable)
		}
	})

	t.Run("ValidateName valid", func(t *testing.T) {
		valid := []string{"foo", "Foo_Bar", "a1b2c3", "meter.power", "_underscore"}
		for _, name := range valid {
			if err := ValidateName(name); err != nil {
				t.Errorf("expected %q to be valid, got error: %v", name, err)
			}
		}
	})

	t.Run("ValidateName invalid", func(t *testing.T) {
		invalid := []string{"", "foo-bar", "foo bar", "foo.bar.baz", ".foo", "foo.", "a@b"}
		for _, name := range invalid {
			if err := ValidateName(name); err == nil {
				t.Errorf("expected %q to be invalid", name)
			}
		}
	})
}

func TestNumberFormatValidation(t *testing.T) {
	valid := []string{
		"0", "123", "-456", "+789", "12.34", "-0.5", ".5", "-.5", "+.5",
		"12345678.123", "-12345678.123", // max digits: 8 before, 3 after
		"99999999.999",
	}
	for _, s := range valid {
		if !isValidNumberFormat(s) {
			t.Errorf("expected %q to be valid", s)
		}
	}

	invalid := []string{
		"", "-", "+", ".", "1e5", "1E5", "1.2.3", "abc", "12abc",
		"123456789",     // 9 digits before decimal
		"1.1234",        // 4 digits after decimal
		"123456789.123", // 9 before, 3 after
		"12345678.1234", // 8 before, 4 after
	}
	for _, s := range invalid {
		if isValidNumberFormat(s) {
			t.Errorf("expected %q to be invalid", s)
		}
	}
}

// Helper function for tests
func mustNumber(f float64) Value {
	v, err := Number(f)
	if err != nil {
		panic(err)
	}
	return v
}
