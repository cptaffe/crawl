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
	"sort"
	"sync"
	"time"
)

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
			case html.StartTagToken, html.EndTagToken:
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

type pageList []Page

func (p pageList) Swap(i, j int) {
	p[i], p[j] = p[j], p[i]
}

func (p pageList) Len() int {
	return len(p)
}

func (p pageList) Less(i, j int) bool {
	return p[i].Views < p[j].Views
}

func main() {
	urloverseer := make(chan *Page)

	frequencyMap := make(map[url.URL]Page)

	// Page pipeline
	pages := ConnectPages(urloverseer)
	urls := LinkGrepPages(pages)

	if len(os.Args) > 1 {
		for _, arg := range os.Args {
			ur, err := url.Parse(arg)
			if err != nil {
				log.Fatal(err)
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

	for {
		select {
		case <-done:
			fmt.Println("Preparing results")
			pages := make(pageList, len(frequencyMap))
			i := 0
			for u, p := range frequencyMap {
				pages[i] = p
				// Save memory by removing
				delete(frequencyMap, u)
				i++
			}
			sort.Sort(pages)

			sz := 0
			if len(pages) > 100 {
				sz = len(pages) - 100
			}

			for i, p := range pages[sz:] {
				fmt.Printf("%d: Views %d: %s\n", 100-i, p.Views, p.Url)
			}
			return
		case page := <-urls:

			// Only request absolute urls, and no stupid javascript
			js := "javascript"
			if !page.Url.IsAbs() || (len(page.Url.String()) >= len(js) &&  page.Url.String()[:len(js)] == js) {
				break
			}

			// Add to map
			p := frequencyMap[*page.Url]
			// Update view count
			page.Views = p.Views + 1
			frequencyMap[*page.Url] = *page

			// fmt.Printf("Url (%d): %s\n", page.Views, page.Url.String())

			// Only visit a website once
			if page.Views == 1 {
				urloverseer <- page
			}
		}
	}
}
