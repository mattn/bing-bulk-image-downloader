package main

import (
	"crypto/md5"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
)

func worker(mu *sync.Mutex, wg *sync.WaitGroup, q chan string, tmpdir, outdir string, count *int64) {
	defer wg.Done()
	for {
		url, ok := <-q
		if !ok {
			return
		}

		log.Print("Download: ", url)
		resp, err := http.Get(url)
		if err != nil {
			log.Print("Download Fail: ", url)
			continue
		}
		if resp.StatusCode != 200 {
			log.Print("Download Fail: ", resp.Status)
			continue
		}
		ct := resp.Header.Get("content-type")
		if !strings.HasPrefix(ct, "image/") {
			log.Print("Download Fail: ", ct)
			continue
		}
		ext := ".jpg"
		switch ct {
		case "image/jpeg", "image/jpg":
			ext = ".jpg"
		case "image/gif":
			ext = ".gif"
		case "image/png":
			ext = ".png"
		}

		fname := fmt.Sprintf("%x", md5.Sum([]byte(url))) + ext
		oldname := filepath.Join(tmpdir, fname)
		f, err := os.Create(oldname)
		if err != nil {
			resp.Body.Close()
			log.Print("Create Fail: ", url)
			continue
		}
		_, err = io.Copy(f, resp.Body)
		if err != nil {
			resp.Body.Close()
			f.Close()
			log.Print("Copy Fail: ", url)
			continue
		}
		f.Close()
		resp.Body.Close()

		mu.Lock()
		n := atomic.AddInt64(count, -1)
		if n >= 0 {
			os.Rename(oldname, filepath.Join(outdir, fmt.Sprintf("%05d-", n+1)+fname))
		}
		mu.Unlock()
	}
}

func main() {
	var count int64
	var outdir string
	flag.Int64Var(&count, "n", 100, "count")
	flag.StringVar(&outdir, "o", ".", "output directory")
	flag.Parse()

	tmpdir, err := ioutil.TempDir("", "downloader")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(tmpdir)

	var wg sync.WaitGroup

	q := make(chan string, 5)

	var mu sync.Mutex
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go worker(&mu, &wg, q, tmpdir, outdir, &count)
	}

	offset := 0

loop:
	for {
		if atomic.LoadInt64(&count) <= 0 {
			break
		}

		param := url.Values{
			"q":          flag.Args(),
			"count":      {fmt.Sprint(count)},
			"safesearch": {"off"},
			"offset":     {fmt.Sprint(offset)},
		}

		query := "https://www.bing.com/images/search?" + param.Encode()
		req, err := http.NewRequest(http.MethodGet, query, nil)
		if err != nil {
			log.Fatal(err)
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Fedora; Linux x86_64; rv:60.0) Gecko/20100101 Firefox/60.0")
		req.Header.Set("Referer", "referer: https://www.bing.com/")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Fatal(err)
		}
		defer resp.Body.Close()

		b, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Fatal(err)
		}
		m := regexp.MustCompile(`murl&quot;:&quot;(.*?)&quot;`).FindAllStringSubmatch(string(b), -1)
		for _, mm := range m {
			if atomic.LoadInt64(&count) <= 0 {
				break loop
			}
			if mq, err := url.QueryUnescape(mm[1]); err == nil {
				q <- mq
			}
		}
		offset++
	}

	close(q)
	wg.Wait()
}
