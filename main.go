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

func moveFile(destination, source string) (err error) {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()
	fi, err := src.Stat()
	if err != nil {
		return err
	}
	flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	perm := fi.Mode() & os.ModePerm
	dst, err := os.OpenFile(destination, flag, perm)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	if err != nil {
		dst.Close()
		os.Remove(destination)
		return err
	}
	err = dst.Close()
	if err != nil {
		return err
	}
	err = src.Close()
	if err != nil {
		return err
	}
	err = os.Remove(source)
	if err != nil {
		return err
	}
	return nil
}

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
		resp.Body.Close()
		f.Close()
		if err != nil {
			log.Print("Copy Fail: ", url)
			continue
		}

		mu.Lock()
		n := atomic.AddInt64(count, -1)
		if n >= 0 {
			err = moveFile(filepath.Join(outdir, fmt.Sprintf("%05d-", n+1)+fname), oldname)
			if err != nil {
				log.Print("Rename Fail: ", url)
			}
		}
		mu.Unlock()
	}
}

func safesearch_s(b bool) string {
	if b {
		return ""
	}
	return "off"
}

func main() {
	var count int64
	var outdir string
	var safesearch bool
	flag.Int64Var(&count, "n", 100, "count")
	flag.StringVar(&outdir, "o", ".", "output directory")
	flag.BoolVar(&safesearch, "s", true, "safe search")
	flag.Parse()

	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(1)
	}

	err := os.MkdirAll(outdir, 0755)
	if err != nil {
		log.Fatal(err)
	}

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

	first := 0

loop:
	for {
		if atomic.LoadInt64(&count) <= 0 {
			break
		}

		param := url.Values{
			"q":          {strings.Join(flag.Args(), " ")},
			"count":      {fmt.Sprint(count)},
			"safesearch": {safesearch_s(safesearch)},
			"first":      {fmt.Sprint(first)},
			"FORM":       {"HDRSC2"},
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
				first++
			}
		}
	}

	close(q)
	wg.Wait()
}
