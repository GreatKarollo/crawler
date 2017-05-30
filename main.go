package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	log "github.com/llimllib/loglevel"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"time"
	"runtime"
	"github.com/PuerkitoBio/goquery"
	"sync"
	"io"
	"net/url"
)


func init()  {
	runtime.GOMAXPROCS(2)

}


type Websites struct {
	url    string
	images []string
	folder string
}


var crawlerWG sync.WaitGroup
var downloadWG sync.WaitGroup
var boolWG bool = false



type Link struct {
	url   string
	text  string
	depth int
}



type HttpError struct {
	original string
}



func (self Link) String() string {
	return fmt.Sprintf(self.url)
}



func (self Link) Valid() bool {
	if self.depth >= MaxDepth {
		return false
	}

	if len(self.text) == 0 {
		return false
	}
	if len(self.url) == 0 || strings.Contains(strings.ToLower(self.url), "javascript") {
		return false
	}

	return true
}


func (self Link) containsDomain() bool {
	var domain = os.Args[1]
	if  strings.Contains(strings.ToLower(self.url), domain) {
		return true
	}
	return false
}


func (self Link) containsRedirect() bool {
	if  strings.Contains(strings.ToLower(self.url), "redirect") {
		return false
	}
	return true
}


func (self HttpError) Error() string {
	return self.original
}


var MaxDepth = 1

func webLinkScraper(resp *http.Response, depth int, isDomain bool) []Link {
	page := html.NewTokenizer(resp.Body)
	links := []Link{}

	var start *html.Token
	var text string

	for {
		_ = page.Next()
		token := page.Token()
		if token.Type == html.ErrorToken {
			break
		}


		if start != nil && token.Type == html.TextToken {
			text = fmt.Sprintf("%s%s", text, token.Data)
		}

		if token.DataAtom == atom.A {
			switch token.Type {
			case html.StartTagToken:
				if len(token.Attr) > 0 {
					start = &token
				}
			case html.EndTagToken:
				if start == nil {
					log.Warnf("Link End found without Start: %s", text)
					continue
				}

				link := newLink(*start, text, depth)

				switch isDomain {
				case true:
					if link.Valid() && link.containsDomain() {
						links = append(links, link)
						log.Debugf("Link Found %v", link)
					}

					start = nil
					text = ""

				case false:
					if link.Valid() /*&& link.containsRedirect()*/ {
						links = append(links, link)
						log.Debugf("Link Found %v", link)
					}

					start = nil
					text = ""

				}


			}
		}
	}

	log.Debug(links)
	return links
}



func newLink(tag html.Token, text string, depth int) Link {
	link := Link{text: strings.TrimSpace(text), depth: depth}

	for i := range tag.Attr {

		if tag.Attr[i].Key == "href" {
			link.url = strings.TrimSpace(tag.Attr[i].Val)
		}
	}
	return link
}



func downloader(url string) (resp *http.Response, err error) {
	log.Debugf("Downloading %s", url)
	resp, err = http.Get(url)
	if err != nil {
		log.Debugf("Error: %s", err)
		return
	}

	if resp.StatusCode > 299 {
		err = HttpError{fmt.Sprintf("Error (%d): %s", resp.StatusCode, url)}
		log.Debug(err)
		return
	}
	return

}




func recursiveDownload(url string, depth int)  []Link {

	var domainLinks = []Link{}
	done := make(chan bool)



	page, err := downloader(url)
	if err != nil {
		log.Error(err)

	}

	links := webLinkScraper(page, depth, true)

	println("**************")
	println("**************")
	println("DOMAIN URLS")
	println("**************")
	println("**************")

	for _, link := range links {

		go func (link Link) {

			fmt.Println(link)

			domainLinks = append(domainLinks, link)

			if depth+1 < MaxDepth {
				recursiveDownload(link.url, depth+1)
			}

			done <- true

		}(link)


	}

	for range links {
		<-done

	}



	println("**************")
	println("Number of pages on domain found:")



	println(len(domainLinks))

	return domainLinks


}




func linkCrawler(domainUrls []Link, depth int) {
	done := make(chan bool)

	for _, link := range domainUrls {
		log.Debugf("Crawling Links %s", link)

		go func(link Link) {

			page, err := downloader(link.url)
			if err != nil {
				log.Error(err)

			}
			links := webLinkScraper(page, depth, false)
			println("**************")
			println("**************")
			println("DOMAIN URL")
			fmt.Println(link)
			println("**************")
			println("CONTAINS LINKS:")
			fmt.Println(links)


			done <- true
		}(link)
	}

	for range domainUrls {
		<-done

	}

}





func imageCrawler(domainUrls []Link) {

	done := make(chan bool)
	var seedUrls [] string

	for _, link := range domainUrls {

		go func(link Link) {


			seedUrls = append(seedUrls, link.url + " ")

			done <- true
		}(link)
	}

	for range domainUrls {
		<-done

	}



	Site := make([]Websites, len(domainUrls))

	for i, name := range seedUrls {

		if name[:4] != "http" {
			name = "http://" + name
		}
		u, err := url.Parse(name)
		if err != nil {
			log.Debugf("Error: %s", err)
			log.Info("could not fetch page - %s %v", name, err)
		}

		Site[i].folder = u.Host
		Site[i].url = name
		crawlerWG.Add(1)
		go Site[i].crawlImages()
	}


	crawlerWG.Wait()
}





func uniqueOnly(s []string) []string {
	for i := 0; i < len(s); i++ {
		for i2 := i + 1; i2 < len(s); i2++ {
			if s[i] == s[i2] {
				// delete
				s = append(s[:i2], s[i2+1:]...)
				i2--
			}
		}
	}
	return s
}


func (Site *Websites) downloadImages(images []string) {

	defer downloadWG.Done()

	os.Mkdir(Site.folder, os.FileMode(0777))

	Site.images = uniqueOnly(images)

	for _, url := range Site.images {
		if url[:4] != "http" {
			url = "http:" + url
		}
		parts := strings.Split(url, "/")
		name := parts[len(parts)-1]
		file, _ := os.Create(string(Site.folder + "/" + name))
		resp, _ := http.Get(url)
		io.Copy(file, resp.Body)
		file.Close()
		resp.Body.Close()
		if boolWG == true {
			log.Info("Saving %s \n", Site.folder+"/"+name)
		}
	}
}



func (Site *Websites) crawlImages() {
	defer crawlerWG.Done()

	resp, err := goquery.NewDocument(Site.url)
	if err != nil {
		log.Debugf("Error: %s", err)
		log.Info("ERROR: Failed to crawl \"" + Site.url + "\"\n\n")
		os.Exit(3)
	}


	resp.Find("*").Each(func(index int, item *goquery.Selection) {
		linkTag := item.Find("img")
		link, _ := linkTag.Attr("src")

		if link != "" {
			Site.images = append(Site.images, link)
		}
	})


	fmt.Println("*****************************************")
	fmt.Println("*****************************************")


	fmt.Printf("%s found %d unique images\n", Site.url, len(Site.images))

	pool := len(Site.images) / 3
	if pool > 10 {
		pool = 10
	}

	l := 0
	counter := len(Site.images) / pool

	for i := counter; i < len(Site.images); i += counter {
		downloadWG.Add(1)
		go Site.downloadImages(Site.images[l:i])
		l = i
	}

	downloadWG.Wait()
}

func main() {


	var wg sync.WaitGroup
	wg.Add(2)

	start1 := time.Now()

	var domain = os.Args[1]

	log.SetPriorityString("info")
	log.SetPrefix("web link crawler")

	log.Debug(os.Args)

	if len(os.Args) < 2 {
		log.Fatalln("No domain provided, nothing to crawl")
	}


	var domainLinks = recursiveDownload(domain, 0)


	go func() {
		defer wg.Done()
		linkCrawler(domainLinks ,0)
	}()


	go func() {
		defer wg.Done()
		imageCrawler(domainLinks)
	}()

	wg.Wait()

	elapsed1 := time.Since(start1)
	fmt.Printf("time elapsed is %s \n", elapsed1)
	fmt.Println("\nTerminating Program")


}







