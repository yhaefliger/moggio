package mog

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	_ "github.com/mjibson/mog/codec/nsf"
)

func TestServer(t *testing.T) {
	errs := make(chan error)
	go func() {
		errs <- ListenAndServe(DefaultAddr, "../codec/nsf")
	}()
	time.Sleep(time.Millisecond * 100)
	fetch := func(path string, values url.Values) *http.Response {
		rc := make(chan *http.Response)
		go func() {
			u := &url.URL{
				Scheme:   "http",
				Host:     DefaultAddr,
				Path:     path,
				RawQuery: values.Encode(),
			}
			t.Log("fetching", u)
			resp, err := http.Get(u.String())
			if err != nil {
				errs <- err
				return
			}
			rc <- resp
		}()
		select {
		case <-time.After(time.Second):
			t.Fatal("timeout")
		case err := <-errs:
			t.Fatal(err)
		case resp := <-rc:
			return resp
		}
		panic("unreachable")
	}

	resp := fetch("/list", nil)
	b, _ := ioutil.ReadAll(resp.Body)
	t.Log(string(b))
	songs := make(Songs)
	if err := json.Unmarshal(b, &songs); err != nil {
		t.Fatal(err)
	}
	v := make(url.Values)
	for i, _ := range songs {
		v.Add("add", strconv.Itoa(i))
		break
	}
	if len(v) == 0 {
		t.Fatal("expected songs")
	}
	resp = fetch("/playlist/change", v)
	b, _ = ioutil.ReadAll(resp.Body)
	t.Log(string(b))
	var pc PlaylistChange
	if err := json.Unmarshal(b, &pc); err != nil {
		t.Fatal(err)
	}
	log.Println("pc", pc)
	resp = fetch("/playlist/get", nil)
	b, _ = ioutil.ReadAll(resp.Body)
	t.Log(string(b))
	var pl Playlist
	if err := json.Unmarshal(b, &pl); err != nil {
		t.Fatal(err)
	}
	for i, v := range pl {
		log.Println("playlist", i, v)
	}
}
