package main

import (
	"sync"
	"io"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"golang.org/x/net/html"
	"os"
	"os/signal"
	"sort"
)

var mapChanges chan func(m map[url.URL]Page)

type Page struct {
	Url *url.URL
	Body io.ReadCloser
	Links []*Page
	Views int
}

func (page *Page) get() error {
	r, err := http.Get(page.Url.String())
	if err != nil {
		return err
	}

	page.Body = r.Body

	return nil
}

func (page *Page) genLinks(out chan<- *Page) {
	go func() {
		defer page.Body.Close()
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

							go func() {
								mapChanges <-func(m map[url.URL]Page) {
									// Only one copy of the page exits,
									// and it lives in the map
									p := m[*ur]
									// Update it with the new url
									// in case it isn't set
									p.Url = ur
									p.Views++
									m[*ur] = p
									// Add link to this page's children
									page.Links = append(page.Links, &p)
									// Send it on to be updated further
									// if it hasn't been seen yet
									if p.Views == 0 {
										out <- &p
									}
								}
							}()
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

func LinkGrepPages(in <-chan *Page) <-chan *Page  {
	ch := make(chan *Page)

	// Create goroutines to push ints to channel
	// regardless of order recieved
	go func() {
		for page := range in {
			// Parse html for anchor tags
			page.genLinks(ch)
		}
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

	mapChanges = make(chan func(m map[url.URL]Page))
	frequencyMap := make(map[url.URL]Page)
	go func() {
		for f := range mapChanges {
			f(frequencyMap)
		}
	}()

	// Page pipeline
	pages := ConnectPages(urloverseer)
	urls := LinkGrepPages(pages)

	var rootPage *Page
	if len(os.Args) == 2 {
		ur, err := url.Parse(os.Args[1])
		if err != nil {
			log.Fatal(err)
		}

		// Initial page to start crawler
		rootPage = &Page{Url: ur}
		urloverseer <- rootPage
	} else {
		fmt.Println("Needs url to start crawler")
		os.Exit(1)
	}

	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt)

	for {
		select {
		case _ = <-c:
			pages := make(pageList, len(frequencyMap))
			i := 0
			for _, p := range frequencyMap {
				pages[i] = p
				i++
			}
			sort.Sort(pages)

			for i, p := range pages {
				fmt.Printf("%d: Views %d: %s\n", i, p.Views, p.Url)
			}

			return
		case page := <- urls:
			urloverseer <- page
		}
	}
}
