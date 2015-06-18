package main

import (
	"fmt"
	"golang.org/x/net/html"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"container/heap"
	"sync"
	"time"
)

var displayTopN = int(10)
var requestTimeout = time.Duration(5 * time.Second)

type Page struct {
	Url   *url.URL
	Body  io.ReadCloser
	Views int
}

func (page *Page) get() error {
	transport := http.Transport{
		Dial: func(network, addr string) (net.Conn, error) {
			return net.DialTimeout(network, addr, requestTimeout)
		},
	}

	client := http.Client{
		Transport: &transport,
	}

	resp, err := client.Get(page.Url.String())
	if err != nil {
		return err
	}

	page.Body = resp.Body

	return nil
}

func (page *Page) genLinks(wg sync.WaitGroup, out chan<- *Page) {
	go func() {
		defer page.Body.Close()
		defer wg.Done()
		z := html.NewTokenizer(page.Body)
		for {
			tt := z.Next()
			switch tt {
			case html.ErrorToken:
				return
			case html.StartTagToken:
				tn, _ := z.TagName()
				if len(tn) == 1 && tn[0] == 'a' {
					more := true
					for more {
						key, val, m := z.TagAttr()
						more = m
						if string(key) == "href" {
							// Parse URL
							ur, err := page.Url.Parse(string(val))
							if err != nil {
								break
							}

							var p Page
							p.Url = ur
							out <- &p
						}
					}
				}
			}
		}
	}()
}

func ConnectPages(pages <-chan *Page) <-chan *Page {
	ch := make(chan *Page)

	// Create goroutines to push ints to channel
	// regardless of order recieved
	go func() {
		var wg sync.WaitGroup
		for page := range pages {
			c := make(chan *Page)
			wg.Add(1)
			go func() {
				defer wg.Done() // call wg.Done() after doIO completes
				page := <-c
				// Update page by adding page source
				err := page.get()
				if err != nil {
					return
				}
				ch <- page
			}()
			c <- page
		}

		// Close channel as soon as all the goroutines
		// above have completed
		wg.Wait()
		close(ch)
	}()

	// return as a read-only channel
	return ch
}

func LinkGrepPages(in <-chan *Page) <-chan *Page {
	ch := make(chan *Page)
	var wg sync.WaitGroup

	// Create goroutines to push ints to channel
	// regardless of order recieved
	go func() {
		for page := range in {
			wg.Add(1)
			// Parse html for anchor tags
			page.genLinks(wg, ch)
		}

		wg.Wait()
		close(ch)
	}()

	return ch
}

type pageHeap []*Page

func (p pageHeap) Swap(i, j int) {
	p[i], p[j] = p[j], p[i]
}

func (p pageHeap) Len() int {
	return len(p)
}

func (p pageHeap) Less(i, j int) bool {
	return p[i].Views < p[j].Views
}

func (p *pageHeap) Push(x interface{}) {
	*p = append(*p, x.(*Page))
}

func (p *pageHeap) Pop() interface{} {
	old := *p
	n := len(old)
	x := old[n-1]
	*p = old[:n-1]
	return x
}

func main() {
	urloverseer := make(chan *Page)

	frequencyMap := make(map[url.URL]*Page)

	// Page pipeline
	pages := ConnectPages(urloverseer)
	urls := LinkGrepPages(pages)

	if len(os.Args) > 1 {
		for _, arg := range os.Args[1:] {
			ur, err := url.Parse(arg)
			if err != nil {
				log.Fatal(fmt.Sprintf("Can't parse url \"%s\": %s", err))
			}

			// Initial page to start crawler
			urloverseer <- &Page{Url: ur}
		}
	} else {
		fmt.Println("Needs url to start crawler")
		return
	}

	done := make(chan os.Signal)
	signal.Notify(done, os.Interrupt)


	totalViews := 0
	for {
		select {
		case <-done:
			fmt.Println("Preparing results")
			pages := &pageHeap{}

			heap.Init(pages)

			i := 0
			for u, p := range frequencyMap {
				heap.Push(pages, p)
				// Save memory by removing
				delete(frequencyMap, u)

				if (i > displayTopN) {
					heap.Pop(pages)
				}
				i++
			}

			for pages.Len() > 0 {
				p := heap.Pop(pages).(*Page)
				fmt.Printf("%d: Views %d: %s\n", displayTopN-pages.Len(), p.Views, p.Url)
			}
			return
		case page := <-urls:

			// Only request absolute urls, and no stupid javascript
			js := "javascript"
			if !page.Url.IsAbs() || (len(page.Url.String()) >= len(js) &&  page.Url.String()[:len(js)] == js) {
				break
			}

			// Add to map
			if frequencyMap[*page.Url] == nil {
				// Update view count
				frequencyMap[*page.Url] = page
			}
			frequencyMap[*page.Url].Views++
			totalViews++
			fmt.Printf("Total Views: %d\r", totalViews)

			// fmt.Printf("Url (%d): %s\n", page.Views, page.Url.String())

			// Only visit a website once
			if page.Views == 0 {
				urloverseer <- page
			}
		}
	}
}
