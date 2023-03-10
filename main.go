package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
	"go.opentelemetry.io/otel/trace"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	wd := flag.String("wd", ".", "Current working directory")
	jaegerURL := flag.String("jaeger.url", "http://localhost:14268/api/traces", "URL for collecting Jaeger traces")
	flag.Parse()
	path := flag.Arg(0)
	if path == "" {
		panic("must provide a path")
	}

	tp, err := tracerProvider(*jaegerURL)
	if err != nil {
		panic(err)
	}

	err = os.Chdir(*wd)
	if err != nil {
		panic(err)
	}

	cmd := exec.Command("go", "test", "-count=1", "-json", "-short", path)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		panic(err)
	}
	defer pipe.Close()
	err = cmd.Start()
	if err != nil {
		panic(err)
	}
	tracer := tp.Tracer("test/go")
	ctx, span := tracer.Start(context.Background(), "test/go")
	testlines := map[string][]GoTestLine{}
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		line := GoTestLine{}
		err := json.Unmarshal(scanner.Bytes(), &line)
		if err != nil {
			panic(err)
		}

		if line.Package != "" {
			key := line.Package
			if _, exists := testlines[key]; !exists {
				testlines[key] = make([]GoTestLine, 0)
			}
			testlines[key] = append(testlines[key], line)
		}

		if line.Action == "output" {
			fmt.Print(line.Output)
		}
	}

	reportSpans(testlines, tracer, ctx)

	fmt.Println(generateComment(testlines))

	fmt.Println(span.SpanContext().TraceID())
	span.End()
	err = tp.ForceFlush(context.Background())
	if err != nil {
		panic(err)
	}

	err = cmd.Wait()
	if err != nil {
		panic(err)
	}
}

type hierarchy struct {
	name     string
	children map[string]*hierarchy
}

func (h *hierarchy) Add(c string) bool {
	if !strings.HasPrefix(c, h.name) {
		return false
	}
	trimmedName := strings.TrimPrefix(c, h.name)
	trimmedName = strings.TrimPrefix(trimmedName, "/")
	parts := strings.Split(trimmedName, "/")
	if len(parts) == 1 {
		if _, exists := h.children[c]; exists {
			return true
		}

		h.children[c] = &hierarchy{
			name:     c,
			children: map[string]*hierarchy{},
		}
		return true
	}

	firstLevelChild := h.name + "/" + parts[0]
	if _, exists := h.children[firstLevelChild]; !exists {
		h.children[firstLevelChild] = &hierarchy{
			name:     c,
			children: map[string]*hierarchy{},
		}
	}
	return h.children[firstLevelChild].Add(c)
}

func (h *hierarchy) Walk(ctx context.Context, fn func(ctx context.Context, tree *hierarchy) context.Context) {
	nestCtx := fn(ctx, h)
	fmt.Println(h.name)
	for _, c := range h.children {
		c.Walk(nestCtx, fn)
	}
}

func reportSpans(testlines map[string][]GoTestLine, tracer trace.Tracer, ctx context.Context) {
	const prefixPlaceholder = "REPLACE_ME"
	commonPrefix := prefixPlaceholder

	for pkg, _ := range testlines {
		if commonPrefix == prefixPlaceholder {
			commonPrefix = pkg
		}
		if strings.HasPrefix(pkg, commonPrefix) {
			continue
		}

		for !strings.HasPrefix(pkg, commonPrefix) {
			commonPrefix = commonPrefix[:len(commonPrefix)-1]
		}
	}

	root := hierarchy{name: commonPrefix, children: map[string]*hierarchy{}}
	for pkg, _ := range testlines {
		root.Add(pkg)
	}

	root.Walk(ctx, func(ctx context.Context, tree *hierarchy) context.Context {
		lines, exists := testlines[tree.name]

		if !exists {
			var start, end time.Time
			tree.Walk(ctx, func(ctx context.Context, tree *hierarchy) context.Context {
				for _, line := range testlines[tree.name] {
					if start.IsZero() || start.After(line.Time) {
						start = line.Time
					}
					if end.Before(line.Time) {
						end = line.Time
					}
				}
				return ctx
			})

			s := newTestSpan(tree.name, "")
			s.start = start
			s.end = end
			return s.Report(ctx, tracer)
		}

		return reportSpan(ctx, tracer, tree.name, lines)
	})
}

func tracerProvider(url string) (*tracesdk.TracerProvider, error) {
	// Create the Jaeger exporter
	exp, err := jaeger.New(jaeger.WithCollectorEndpoint(jaeger.WithEndpoint(url)))
	if err != nil {
		return nil, err
	}
	tp := tracesdk.NewTracerProvider(
		// Always be sure to batch in production.
		tracesdk.WithBatcher(exp),
		// Record information about this application in a Resource.
		tracesdk.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("go-test-runner"),
		)),
	)
	return tp, nil
}
