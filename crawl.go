package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

const maxFetches = 10

type Page struct {
	Resource string  `json:"resource"`
	Title    string  `json:"title"`
	Links    []*Page `json:"links"`
}

type job struct {
	url    string
	step   int
	site   string
	parent *Page
}

type crawler struct {
	mu             sync.Mutex
	wg             sync.WaitGroup
	jobs           chan job
	workers        int
	seen           map[string]bool
	roots          []*Page
	startJobs      []job
	depth          int
	requestTimeout time.Duration
	log            *log.Logger
	client         *http.Client
}

func newCrawler(urls []string, depth, workers int, requestTimeout time.Duration, logger *log.Logger) *crawler {
	if workers <= 0 {
		workers = maxFetches
	}
	if requestTimeout <= 0 {
		requestTimeout = 10 * time.Second
	}
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	c := &crawler{
		jobs:           make(chan job, 10000),
		workers:        workers,
		seen:           make(map[string]bool),
		roots:          make([]*Page, 0),
		depth:          depth,
		requestTimeout: requestTimeout,
		log:            logger,
		client: &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	for _, raw := range urls {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		site, err := startHost(raw)
		if err != nil {
			c.log.Printf("error url=%s err=%v", raw, err)
			continue
		}
		if _, ok := c.seen[raw]; ok {
			continue
		}
		c.seen[raw] = true
		c.startJobs = append(c.startJobs, job{url: raw, step: 0, site: site})
	}
	return c
}

func (c *crawler) run(ctx context.Context) ([]*Page, error) {
	if len(c.startJobs) == 0 {
		return c.roots, fmt.Errorf("нет нормальных стартовых URL")
	}
	for i := 0; i < c.workers; i++ {
		go c.worker(ctx)
	}
	for _, j := range c.startJobs {
		c.enqueue(j)
	}
	c.wg.Wait()
	close(c.jobs)
	if err := ctx.Err(); err != nil {
		return c.roots, err
	}
	return c.roots, nil
}

func (c *crawler) enqueue(j job) {
	c.wg.Add(1)
	c.jobs <- j
}

func (c *crawler) worker(ctx context.Context) {
	for j := range c.jobs {
		c.handle(ctx, j)
		c.wg.Done()
	}
}

func (c *crawler) handle(ctx context.Context, j job) {
	if ctx.Err() != nil {
		return
	}

	title, links, ok := c.download(ctx, j)
	if !ok {
		return
	}

	p := &Page{
		Resource: j.url,
		Title:    title,
		Links:    []*Page{},
	}
	var next []job

	c.mu.Lock()
	if j.parent == nil {
		c.roots = append(c.roots, p)
	} else {
		j.parent.Links = append(j.parent.Links, p)
	}
	if j.step < c.depth {
		for _, link := range links {
			if !sameHost(link, j.site) {
				continue
			}
			if c.seen[link] {
				continue
			}
			c.seen[link] = true
			next = append(next, job{
				url:    link,
				step:   j.step + 1,
				site:   j.site,
				parent: p,
			})
		}
	}
	c.mu.Unlock()

	for _, n := range next {
		c.enqueue(n)
	}
}

func (c *crawler) download(ctx context.Context, j job) (string, []string, bool) {
	reqCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	req, err := http.NewRequest("GET", j.url, nil)
	if err != nil {
		c.log.Printf("error url=%s err=%v", j.url, err)
		return "", nil, false
	}
	req = req.WithContext(reqCtx)
	req.Header.Set("User-Agent", "crawler-cli")

	resp, err := c.client.Do(req)
	if err != nil {
		c.log.Printf("error url=%s err=%v", j.url, err)
		return "", nil, false
	}
	defer resp.Body.Close()

	c.log.Printf("status=%d url=%s", resp.StatusCode, j.url)
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		c.log.Printf("skip redirect url=%s location=%s", j.url, resp.Header.Get("Location"))
		return "", nil, false
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, false
	}

	ct := resp.Header.Get("Content-Type")
	if !isHTML(ct) {
		c.log.Printf("skip non-html url=%s content-type=%q", j.url, ct)
		return "", nil, false
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.log.Printf("error url=%s err=%v", j.url, err)
		return "", nil, false
	}
	title, links, err := parsePage(j.url, bytes.NewReader(body))
	if err != nil {
		c.log.Printf("error url=%s err=%v", j.url, err)
		return "", nil, false
	}
	return title, links, true
}

func startHost(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("схема %q не поддерживается", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("нет хоста")
	}
	return u.Host, nil
}

func sameHost(raw, site string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return sameSite(u.Host, site)
}

func sameSite(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a = strings.ToLower(a)
	b = strings.ToLower(b)
	a = strings.TrimPrefix(a, "www.")
	b = strings.TrimPrefix(b, "www.")
	return a == b
}

func isHTML(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "html")
}

func parsePage(pageURL string, r io.Reader) (string, []string, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return "", nil, err
	}

	var title string
	var links []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "title" && title == "" {
			title = strings.Join(strings.Fields(textOf(n)), " ")
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			href := ""
			for _, a := range n.Attr {
				if a.Key == "href" {
					href = a.Val
					break
				}
			}
			if abs := absURL(pageURL, href); abs != "" {
				links = append(links, abs)
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if links == nil {
		links = []string{}
	}
	return title, links, nil
}

func absURL(pageURL, href string) string {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "#") {
		return ""
	}
	if strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "javascript:") {
		return ""
	}
	base, err := url.Parse(pageURL)
	if err != nil {
		return ""
	}
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	abs := base.ResolveReference(ref)
	if (abs.Scheme != "http" && abs.Scheme != "https") || abs.Host == "" {
		return ""
	}
	return abs.String()
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return b.String()
}
