package main

import (
	//"context"
	"log"
	"net/http"
	"time"
)

type HookHandler struct {
}

func (HookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	//ctx := context.Background()
	start := time.Now()

	var status int
	if r.Method != "POST" {
		status = http.StatusBadRequest
	} else {
		status = http.StatusNotFound
	}

	w.WriteHeader(status)
	log.Printf("%s [%d] in %.2fms", r.URL.Path, status, float64(time.Since(start))/float64(time.Millisecond))
}

func main() {
	server := &http.Server{
		Addr:           ":8283",
		Handler:        HookHandler{},
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	err := server.ListenAndServe()
	if err != nil {
		log.Fatal("could not start server,", err)
	}
}
