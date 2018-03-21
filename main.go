package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"time"
	//"encoding/json"

	"github.com/go-ini/ini"
	"github.com/pkg/errors"
)

type Repo struct {
	Name   string
	Secret string `ini:"secret"`
}

type GithubPayload struct {
	Repository struct {
		Name string `json:"name"`
	} `json:"repository"`
}

type HookHandler struct {
	Repos []Repo
}

func (h HookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Server", "spectacle")

	start := time.Now()
	defer func() {
		log.Printf("└%s in %.2fms", r.URL.Path, float64(time.Since(start))/float64(time.Millisecond))
	}()

	if r.URL.Path != "/hook" {
		http.Error(w, "404 not found", http.StatusNotFound)
		return
	} else if r.Method != "POST" {
		http.Error(w, "405 forbidden", http.StatusMethodNotAllowed)
		return
	} else if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "400 bad request", http.StatusBadRequest)
		return
	} else if len(r.Header.Get("X-Hub-Signature")) < 45 {
		http.Error(w, "400 bad request", http.StatusBadRequest)
		return
	}

	raw, _ := ioutil.ReadAll(r.Body)

	payload := GithubPayload{}
	err := json.Unmarshal(raw, &payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Find config
	repo, ok := (func() (*Repo, bool) {
		for _, repo := range h.Repos {
			if repo.Name == payload.Repository.Name {
				return &repo, true
			}
		}
		return nil, false
	})()

	if !ok {
		http.Error(w, "400 bad request", http.StatusBadRequest)
		return
	}

	// Verify signature
	mac := hmac.New(sha1.New, []byte(repo.Secret))
	mac.Write(raw)
	sum := mac.Sum(nil)
	actual := make([]byte, 20)
	hex.Decode(actual, []byte(r.Header.Get("X-Hub-Signature")[5:]))
	if !hmac.Equal(sum, actual) {
		http.Error(w, "403 forbidden", http.StatusForbidden)
		return
	}

	w.WriteHeader(http.StatusNoContent)
	event := r.Header.Get("X-GitHub-Event")
	log.Printf("┌incoming hook: %s|%s\n", repo.Name, event)
}

func main() {
	handler := HookHandler{
		Repos: make([]Repo, 0, 10),
	}

	cfg, err := ini.Load("spectacle.ini")
	if err != nil {
		log.Fatal(errors.Wrap(err, "could not read config"))
	}
	cfg.BlockMode = false

	for _, section := range cfg.Sections() {
		name := section.Name()
		if name == "DEFAULT" {
			continue
		}

		repo := Repo{
			Name: name,
		}
		if err := section.MapTo(&repo); err != nil {
			log.Fatal(errors.Wrap(err, "failed to map repo config"))
		}
		handler.Repos = append(handler.Repos, repo)
	}

	names := []string{}
	for _, repo := range handler.Repos {
		names = append(names, repo.Name)
	}
	log.Println("registered repos:", strings.Join(names, ", "))

	server := &http.Server{
		Addr:           ":8283",
		Handler:        handler,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	err = server.ListenAndServe()
	if err != nil {
		log.Fatal("could not start server,", err)
	}
}