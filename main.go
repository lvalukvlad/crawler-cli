package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	urlsFlag := flag.String("urls", "", "URL через запятую")
	depth := flag.Int("depth", 1, "глубина")
	timeout := flag.Duration("timeout", time.Minute, "общий таймаут")
	requestTimeout := flag.Duration("request-timeout", 10*time.Second, "таймаут запроса")
	output := flag.String("output", "result.json", "json")
	logPath := flag.String("log", "crawler.log", "лог")
	flag.Parse()

	if strings.TrimSpace(*urlsFlag) == "" {
		fmt.Fprintln(os.Stderr, "нужен --urls")
		os.Exit(1)
	}

	logFile, err := os.Create(*logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "не открыл лог: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()

	c := newCrawler(splitURLs(*urlsFlag), *depth, maxFetches, *requestTimeout, log.New(logFile, "", log.LstdFlags))

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		cancel()
	}()

	pages, runErr := c.run(ctx)
	if err := writeJSON(*output, pages); err != nil {
		fmt.Fprintf(os.Stderr, "не записал результат: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("готово, смотри %s\n", *output)
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "обход остановлен: %v\n", runErr)
	}
}

func splitURLs(s string) []string {
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func writeJSON(path string, pages []*Page) error {
	data, err := json.MarshalIndent(pages, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
