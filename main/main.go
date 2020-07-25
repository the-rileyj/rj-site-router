package main

import (
	"flag"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

func main() {
	development := flag.Bool("d", false, "development flag")

	flag.Parse()

	r := gin.Default()

	forwardHosts := map[string]string{
		"/test": "http://test:80",
	}

	var (
		err     error
		hostURL *url.URL
	)

	// Marshal from file, add routes with Auth to create, read, edit, delete paths
	forwardHostPrefixToURLs := make(map[string]*url.URL)

	for hostPathPrefix, forwardHost := range forwardHosts {
		hostURL, err = url.Parse(forwardHost)

		if err != nil {
			panic(err)
		}

		forwardHostPrefixToURLs[hostPathPrefix] = hostURL
	}

	// Default to serving homepage
	proxy := &httputil.ReverseProxy{Director: func(request *http.Request) {
		originHost := "httpbin.org"
		originPathPrefix := "/anything"

		request.Header.Add("X-Forwarded-Host", request.Host)
		request.Header.Add("X-Origin-Host", originHost)
		request.Host = originHost
		request.URL.Scheme = "https"
		request.URL.Host = originHost
		request.URL.Path = originPathPrefix + request.URL.Path

		///

		slashIndex := strings.Index(request.URL.Path[1:], "/")

		pathPrefix := request.URL.Path

		if slashIndex != -1 {
			pathPrefix = pathPrefix[:slashIndex+1]
		}

		hostURL, exists := forwardHostPrefixToURLs[pathPrefix]

		if !exists {
			// Send to homepage...

			return
		}

		request.URL = hostURL
	}}

	r.GET("/", func(c *gin.Context) {
		c.Writer.Write([]byte("WOW CI is sorta WORKING!?!"))
	})

	r.NoRoute(func(c *gin.Context) {
		proxy.ServeHTTP(c.Writer, c.Request)
	})

	if *development {
		r.Run(":80")
	} else {
		r.RunTLS(":443", "./secrets/cloudflare.crt", "./secrets/cloudflare.secret")
	}
}
