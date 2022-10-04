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
	tracer := tp.Tracer("test/go")
	ctx, span := tracer.Start(context.Background(), "go tests")
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

func reportSpans(testlines map[string][]GoTestLine, tracer trace.Tracer, ctx context.Context) {
	for pkg, lines := range testlines {
		reportSpan(ctx, tracer, pkg, lines)
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
