package main

import (
	"context"
	"fmt"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"strings"
	"time"
)

type testSpan struct {
	pkg      string
	test     string
	failed   bool
	skipped  bool
	start    time.Time
	end      time.Time
	output   []GoTestLine
	children map[string]*testSpan
}

func newTestSpan(pkg string, test string) *testSpan {
	return &testSpan{
		pkg:      pkg,
		test:     test,
		start:    time.Time{},
		end:      time.Time{},
		output:   []GoTestLine{},
		children: map[string]*testSpan{},
	}
}

func (t *testSpan) Add(depth int, line GoTestLine) {
	if !line.Time.IsZero() {
		if line.Time.Before(t.start) || t.start.IsZero() {
			t.start = line.Time
		}
		if line.Time.After(t.end) {
			t.end = line.Time
		}
	}

	if line.Test != "" {
		testParts := strings.Split(line.Test, "/")
		if len(testParts) > depth {
			childName := strings.Join(testParts[:depth+1], "/")

			if _, exists := t.children[childName]; !exists {
				t.children[childName] = newTestSpan(line.Package, childName)
			}
			t.children[childName].Add(depth+1, line)
			return
		}
	}

	switch line.Action {
	case "output":
		t.output = append(t.output, line)
	case "run":
		if line.Time.IsZero() {
			return
		}
		t.start = line.Time
	case "skip":
		t.skipped = true
	case "fail":
		t.failed = true
		fallthrough
	case "pass":
		if line.Time.IsZero() {
			return
		}
		if t.start.IsZero() {
			t.start = line.Time.Add(-time.Duration(line.Elapsed))
		}
		t.end = line.Time
	}
}

func (t *testSpan) Report(ctx context.Context, tracer trace.Tracer) {
	spanName := "test/run"
	if t.test == "" {
		spanName = "test/package"
	}
	if t.skipped {
		spanName = "test/skipped"
	}

	ctx, span := tracer.Start(ctx, spanName, trace.WithTimestamp(t.start))
	defer span.End(trace.WithTimestamp(t.end))

	span.SetAttributes(
		attribute.Key("package").String(t.pkg),
		attribute.Key("test").String(t.test),
	)

	if t.failed {
		span.SetStatus(codes.Error, "test failed")
	} else if !t.skipped {
		span.SetStatus(codes.Ok, "test passed")
	}

	for _, line := range t.output {
		span.AddEvent(
			"log message",
			trace.WithTimestamp(line.Time),
			trace.WithAttributes(attribute.Key("output").String(line.Output)),
		)
	}

	for _, child := range t.children {
		child.Report(ctx, tracer)
	}
}

func reportSpan(ctx context.Context, tracer trace.Tracer, pkg string, lines []GoTestLine) {
	s := newTestSpan(pkg, "")
	for _, line := range lines {
		s.Add(0, line)
	}

	if s.start.IsZero() || s.end.IsZero() {
		fmt.Println("either start or end is zero :(", s.start, s.end)
		return
	}

	s.Report(ctx, tracer)
}
