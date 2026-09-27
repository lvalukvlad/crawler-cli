package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParsePage(t *testing.T) {
	body := `<html><head><title>Hello world</title></head><body>
	<a href="/about">about</a>
	<a href="rel">rel</a>
	<a href="mailto:a@b.c">mail</a>
	<a href="https://other.test/x">out</a>
	</body></html>`

	title, links, err := parsePage("https://example.com/dir/page", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if title != "Hello world" {
		t.Fatalf("title %q", title)
	}
	if len(links) != 3 {
		t.Fatalf("links %#v", links)
	}
	if links[0] != "https://example.com/about" {
		t.Fatalf("abs link %q", links[0])
	}
	if links[1] != "https://example.com/dir/rel" {
		t.Fatalf("rel link %q", links[1])
	}
}

func TestCrawl(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<html><title>Home</title><a href="/a">a</a><a href="/a">a2</a><a href="https://evil.test/no">e</a></html>`)
		case "/a":
			fmt.Fprint(w, `<html><title>A</title><a href="/b">b</a></html>`)
		case "/b":
			fmt.Fprint(w, `<html><title>B</title></html>`)
		}
	}))
	defer srv.Close()

	pages, err := newCrawler([]string{srv.URL + "/"}, 1, 2, time.Second, nil).run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || pages[0].Title != "Home" {
		t.Fatalf("root %#v", pages)
	}
	if len(pages[0].Links) != 1 || pages[0].Links[0].Title != "A" {
		t.Fatalf("children %#v", pages[0].Links)
	}
}

func TestCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><title>Slow</title></html>`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err := newCrawler([]string{srv.URL}, 0, 2, 10*time.Second, nil).run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}
