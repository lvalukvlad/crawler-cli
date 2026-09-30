package main

import (
	"context"
	"encoding/json"
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
	<a href="../up">up</a>
	<a href="./here">here</a>
	<a href="?page=2">q</a>
	<a href="//example.com/page">scheme</a>
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
	want := []string{
		"https://example.com/about",
		"https://example.com/dir/rel",
		"https://example.com/up",
		"https://example.com/dir/here",
		"https://example.com/dir/page?page=2",
		"https://example.com/page",
		"https://other.test/x",
	}
	if len(links) != len(want) {
		t.Fatalf("links %#v", links)
	}
	for i := range want {
		if links[i] != want[i] {
			t.Fatalf("link %d got %q want %q", i, links[i], want[i])
		}
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

func TestRedirectsSkipped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<html><title>Home</title>
				<a href="/ok">ok</a>
				<a href="/same">same</a>
				<a href="/out">out</a>
			</html>`)
		case "/ok":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><title>OK</title></html>`)
		case "/same":
			http.Redirect(w, r, "/ok", http.StatusFound)
		case "/out":
			http.Redirect(w, r, "https://evil.test/no", http.StatusMovedPermanently)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	pages, err := newCrawler([]string{srv.URL + "/"}, 1, 2, time.Second, nil).run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("roots %#v", pages)
	}
	if len(pages[0].Links) != 1 || pages[0].Links[0].Title != "OK" {
		t.Fatalf("children %#v", pages[0].Links)
	}

	raw, err := json.Marshal(pages)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, "/same") || strings.Contains(s, "/out") {
		t.Fatalf("redirect url in json: %s", s)
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
