package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
	"go.opentelemetry.io/otel/trace"
	"os"
	"os/exec"
	"time"
)

type keyPkgAndTest struct {
	Package string
	Test    string
}

func main() {
	wd := flag.String("wd", ".", "Current working directory")
	tp, err := tracerProvider("https://tempo.e127.se:443/api/traces")
	if err != nil {
		panic(err)
	}

	flag.Parse()
	path := flag.Arg(0)
	if path == "" {
		panic("must provide a path")
	}

	err = os.Chdir(*wd)
	if err != nil {
		panic(err)
	}

	cmd := exec.Command("go", "test", "-count=1", "-json", path)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		panic(err)
	}
	defer pipe.Close()
	err = cmd.Start()
	if err != nil {
		panic(err)
	}
	tracer := tp.Tracer("go test")
	ctx, span := tracer.Start(context.Background(), "go tests")
	testlines := map[keyPkgAndTest][]GoTestLine{}
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		line := GoTestLine{}
		err := json.Unmarshal(scanner.Bytes(), &line)
		if err != nil {
			panic(err)
		}

		if line.Package != "" && line.Test != "" {
			key := keyPkgAndTest{line.Package, line.Test}
			if _, exists := testlines[key]; !exists {
				testlines[key] = make([]GoTestLine, 0)
			}
			testlines[key] = append(testlines[key], line)
		}

		if line.Action == "output" {
			fmt.Print(line.Output)
		}
	}

	for k, lines := range testlines {
		var start, end time.Time
		failed := false

		for _, t := range lines {
			switch t.Action {
			case "run":
				if t.Time.IsZero() {
					continue
				}
				start = t.Time
			case "fail":
				failed = true
				fallthrough
			case "pass":
				if t.Time.IsZero() {
					continue
				}
				if start.IsZero() {
					start = t.Time.Add(-time.Duration(t.Elapsed))
				}
				end = t.Time
			}
		}

		if start.IsZero() || end.IsZero() {
			fmt.Println("either start or end is zero :(", start, end)
			continue
		}

		_, span := tracer.Start(ctx, "ran test", trace.WithTimestamp(start))
		span.SetAttributes(attribute.Key("package").String(k.Package), attribute.Key("test").String(k.Test))
		if failed {
			span.SetStatus(codes.Error, "test failed")
		}

		for _, t := range lines {
			if t.Action != "output" {
				continue
			}

			span.AddEvent("log message", trace.WithTimestamp(t.Time), trace.WithAttributes(attribute.Key("output").String(t.Output)))
		}

		span.End(trace.WithTimestamp(end))
	}

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
