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

type Page struct {
	Url *url.URL
	Body io.ReadCloser
}

func ReaderGenerator(pages <-chan Page) (<-chan Page, <-chan error) {
	ch := make(chan Page)
	errch := make(chan error)

	// Create goroutines to push ints to channel
	// regardless of order recieved
	go func() {
		var wg sync.WaitGroup
		for page := range pages {
			c := make(chan Page)
			wg.Add(1)
			go func() {
				defer wg.Done() // call wg.Done() after doIO completes
				page := <-c
				r, err := http.Get(page.Url.String())
				if err != nil {
					errch <- err
					return
				}
				page.Body = r.Body
				ch <- page
			}()
			c <- page
		}

		// Close channel as soon as all the goroutines
		// above have completed
		wg.Wait()
		close(ch)
		close(errch)
	}()

	// return as a read-only channel
	return ch, errch
}

func URLGenerator(pages <-chan Page) (<-chan Page, <-chan error) {
	ch := make(chan Page)
	ech := make(chan error)

	// Create goroutines to push ints to channel
	// regardless of order recieved
	go func() {
		var wg sync.WaitGroup
		for pg := range pages {
			c := make(chan Page)
			wg.Add(1)
			go func() {
				defer wg.Done()
				// Parse html for anchor tags
				pg := <-c
				defer pg.Body.Close()
				z := html.NewTokenizer(pg.Body)
				for {
					tt := z.Next()
					switch tt {
					case html.ErrorToken:
						ech <- z.Err()
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
									ur, err := pg.Url.Parse(string(val))
									if err != nil {
										ech <- err
										break
									}
									ch <- Page{Url: ur} // href link
								}
							}
						}
					}
				}
			}()
			c <- pg
		}

		// Close channel as soon as all the goroutines
		// above have completed
		wg.Wait()
		close(ch)
		close(ech)
	}()

	return ch, ech
}

type sortedMap struct {
	m map[string]int
	s []string
}

func (sm *sortedMap) Len() int {
	return len(sm.m)
}

func (sm *sortedMap) Less(i, j int) bool {
	return sm.m[sm.s[i]] > sm.m[sm.s[j]]
}

func (sm *sortedMap) Swap(i, j int) {
	sm.s[i], sm.s[j] = sm.s[j], sm.s[i]
}

func sortedKeys(m map[string]int) []string {
	sm := new(sortedMap)
	sm.m = m
	sm.s = make([]string, len(m))
	i := 0
	for key, _ := range m {
		sm.s[i] = key
		i++
	}
	sort.Sort(sm)
	return sm.s
}

func main() {
	ch := make(chan Page)
	srch := make(chan Page)

	rch, _ := ReaderGenerator(ch)
	sch, _ := URLGenerator(srch)

	if len(os.Args) == 2 {
		ur, err := url.Parse(os.Args[1])
		if err != nil {
			log.Fatal(err)
		}

		// Initial page to start crawler
		ch <- Page{Url: ur}
	} else {
		fmt.Println("Needs url to start crawler")
		os.Exit(1)
	}

	frequencyMap := make(map[string]int)

	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt)

	for {
		select {
		case _ = <-c:
			// Get top 100 url strings
			sk := sortedKeys(frequencyMap)
			for i, str := range sk {
				maxlen := 50
				if i < 100 {
					if (len(str) > maxlen) {
						fmt.Printf("%d: %d Views, %s...\n", i, frequencyMap[str], str[:maxlen - 3])
					} else {
						fmt.Printf("%d: %d Views, %s\n", i, frequencyMap[str], str)
					}
				}
				fd, _ := os.Open("out.log")
				fmt.Fprintf(fd, "%d: %d Views, %s\n", i, frequencyMap[str], str)
			}
			return
		case rdr := <-rch:
			srch <- rdr
		case str := <- sch:
			// keep track of most seen
			visits := frequencyMap[str.Url.String()]
			frequencyMap[str.Url.String()] = visits + 1
			if (visits == 0) {
				// Don't visit something more than once
				ch <- str
			}
		}
	}
}
