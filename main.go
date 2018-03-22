package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/go-ini/ini"
	"github.com/pkg/errors"
)

type Repo struct {
	Name   string
	Secret string `ini:"secret"`
	Branch string `ini:"branch"`
}

type GithubPayload struct {
	Ref        string `json:"ref"`
	Repository struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
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
			if repo.Name == payload.Repository.FullName {
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

	// Handle event
	event := r.Header.Get("X-GitHub-Event")
	logMsg := fmt.Sprintf("┌incoming hook: %s|%s\n", repo.Name, event)
	switch event {
	case "watch":
		logMsg += "├to be implemented\n"
	case "push":
		if !strings.HasSuffix(payload.Ref, repo.Branch) {
			logMsg += fmt.Sprintf("├ignored ref \"%s\"\n", payload.Ref)
			break
		}

		// TODO: Queue and run on goroutine
		tmpDir := fmt.Sprintf("/tmp/spectacle-%s", strings.Replace(repo.Name, "/", "-", -1))
		gitUrl := fmt.Sprintf("https://github.com/%s", repo.Name)
		buildPath := tmpDir + "/src/github.com/" + repo.Name
		if _, err := os.Stat(tmpDir); os.IsExist(err) {
			os.Remove(tmpDir)
		}

		os.Mkdir(tmpDir, os.ModePerm)
		os.MkdirAll(buildPath, os.ModePerm)
		gitCmd := exec.Command("git", "clone", gitUrl, buildPath)
		gitCmd.Env = append(os.Environ(),
			"GOPATH="+tmpDir,
		)
		if err := gitCmd.Run(); err != nil {
			logMsg += fmt.Sprintf("├failed to prepare for build, %s", err.Error())
			http.Error(w, "403 forbidden", http.StatusForbidden)
			return
		}
	default:
		logMsg += "├unhandled\n"
	}
	log.Printf(logMsg)

	w.WriteHeader(http.StatusNoContent)
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

	log.Println("going up...")
	err = server.ListenAndServe()
	if err != nil {
		log.Fatal("could not start server,", err)
	}
}
