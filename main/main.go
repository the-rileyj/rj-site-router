package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/the-rileyj/uyghurs"
)

type domainRoutesManager struct {
	routesMap    map[string]*uyghurs.RouteInfo
	domainRegexp *regexp.Regexp
}

type routesManager struct {
	defaultDomain   string
	domainRoutesMap map[string]*domainRoutesManager
	projectsMap     map[string]*uyghurs.ProjectMetadata
	lock            *sync.Mutex
}

func newRoutesManager(defaultDomain, defaultHost string) *routesManager {
	rM := &routesManager{
		defaultDomain:   defaultDomain,
		domainRoutesMap: make(map[string]*domainRoutesManager),
		projectsMap:     make(map[string]*uyghurs.ProjectMetadata),
		lock:            &sync.Mutex{},
	}

	defaultHostURL, err := url.Parse(defaultHost)

	if err != nil {
		panic(err)
	}

	defaultDomainReverseProxy := httputil.NewSingleHostReverseProxy(defaultHostURL)

	rM.domainRoutesMap[defaultDomain] = &domainRoutesManager{
		routesMap: map[string]*uyghurs.RouteInfo{
			"/": {
				Domain:              defaultDomain,
				ForwardHost:         defaultHost,
				ReverseProxyHandler: func(c *gin.Context) { defaultDomainReverseProxy.ServeHTTP(c.Writer, c.Request) },
				Route:               "/",
			},
		},
		domainRegexp: nil,
	}

	return rM
}

func (rM *routesManager) GetDefaultRouteInfo() *uyghurs.RouteInfo {
	domainRoutesManager, exists := rM.domainRoutesMap[rM.defaultDomain]

	if !exists {
		log.Fatal("NO DEFAULT ROUTE FOR DEFAULT DOMAIN")
	}

	routeInfo, exists := domainRoutesManager.routesMap["/"]

	if !exists {
		log.Fatal("NO DEFAULT ROUTE for '/'")
	}

	return routeInfo
}

func (rM *routesManager) GetRouteInfo(domain, route string) (*uyghurs.RouteInfo, bool) {
	rM.lock.Lock()

	defer rM.lock.Unlock()

	if domain == rM.defaultDomain {
		domainRoutesManager, exists := rM.domainRoutesMap[domain]

		if !exists {
			return nil, false
		}

		routeInfo, exists := domainRoutesManager.routesMap[route]

		return routeInfo, exists
	}

	for _, domainManager := range rM.domainRoutesMap {
		if domainManager.domainRegexp != nil && domainManager.domainRegexp.MatchString(domain) {
			routeInfo, exists := domainManager.routesMap[route]

			return routeInfo, exists
		}
	}

	return nil, false
}

func (rM *routesManager) UpdateProjectRoutes(projectMetadata *uyghurs.ProjectMetadata) {
	rM.lock.Lock()

	defer rM.lock.Unlock()

	currentProjectMetadata, exists := rM.projectsMap[projectMetadata.ProjectName]

	seenRoutes := make(map[string]bool)

	if exists {
		for _, routeInfo := range currentProjectMetadata.ProjectRoutes {
			domain := routeInfo.Domain

			if domain == "" {
				domain = rM.defaultDomain
			}

			if seenRoutes[domain+routeInfo.Route] {
				continue
			}

			seenRoutes[domain+routeInfo.Route] = true

			domainRoutesManager := rM.domainRoutesMap[domain]

			delete(domainRoutesManager.routesMap, routeInfo.Route)

			if len(domainRoutesManager.routesMap) == 0 {
				delete(rM.domainRoutesMap, domain)
			}
		}
	}

	delete(rM.projectsMap, projectMetadata.ProjectName)

	for _, routeInfo := range projectMetadata.ProjectRoutes {
		domainRoutesMan, exists := rM.domainRoutesMap[routeInfo.Domain]

		if !exists {
			domainRegexp, err := regexp.Compile(routeInfo.Domain)

			if err != nil {
				domainRegexp = nil
			}

			domainRoutesMan = &domainRoutesManager{
				routesMap:    make(map[string]*uyghurs.RouteInfo),
				domainRegexp: domainRegexp,
			}
		}

		newRouteHostURL, err := url.Parse(routeInfo.ForwardHost)

		if err != nil {
			log.Printf("Failed to add new route %s: %s\n", fmt.Sprintf(routeInfo.Domain+routeInfo.Route), err)

			continue
		}

		newRouteReverseProxy := httputil.NewSingleHostReverseProxy(newRouteHostURL)

		domainRoutesMan.routesMap[routeInfo.Route] = &uyghurs.RouteInfo{
			Domain:              routeInfo.Domain,
			ReverseProxyHandler: func(c *gin.Context) { newRouteReverseProxy.ServeHTTP(c.Writer, c.Request) },
			ForwardHost:         routeInfo.ForwardHost,
			Route:               routeInfo.Route,
		}

		rM.domainRoutesMap[routeInfo.Domain] = domainRoutesMan
	}

	rM.projectsMap[projectMetadata.ProjectName] = projectMetadata
}

func main() {
	development := flag.Bool("d", false, "development flag")

	defaultDomain := flag.String("dd", "therileyjohnson.com", "the most frequently used domain, roughly the default")
	defaultHost := flag.String("dh", "http://rj-site", "the default host to forward requests to")

	flag.Parse()

	uyghursSecretJSONFile, err := os.Open("secrets/router.json")

	if err != nil {
		panic(err)
	}

	uyghursSecretsJSONData := struct {
		Secret string `json:"secret"`
	}{}

	err = json.NewDecoder(uyghursSecretJSONFile).Decode(&uyghursSecretsJSONData)

	if err != nil {
		panic(err)
	}

	routesManager := newRoutesManager(*defaultDomain, *defaultHost)

	if !*development {
		go func() {
			uyghursURL := url.URL{Scheme: "wss", Host: "therileyjohnson.com:8443", Path: fmt.Sprintf("/router/%s", uyghursSecretsJSONData.Secret)}

			connectIndefinitely := func() net.Conn {
				var (
					conn    net.Conn
					connErr error
				)

				dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

				for conn, _, _, connErr = ws.DefaultDialer.Dial(dialCtx, uyghursURL.String()); connErr != nil; {
					time.Sleep(time.Second)

					cancel()

					dialCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)

					conn, _, _, connErr = ws.DefaultDialer.Dial(dialCtx, uyghursURL.String())
				}

				cancel()

				return conn
			}

			uyghursConnection := connectIndefinitely()

			log.Println("Initial connection to uyghurs")

			var projectsMetadataMessage []*uyghurs.ProjectMetadata

			for {
				messageBytes, _, err := wsutil.ReadServerData(uyghursConnection)

				if err != nil {
					// if websocket.IsCloseError(err, websocket.CloseNoStatusReceived, websocket.CloseAbnormalClosure, websocket.CloseInternalServerErr) {
					if uyghursConnection != nil {
						uyghursConnection.Close()
					}

					uyghursConnection = connectIndefinitely()

					log.Println("Reconnected to uyghurs server!")

					continue
					// } else if websocket.IsCloseError(err, websocket.CloseNoStatusReceived, websocket.CloseGoingAway, websocket.CloseMessage, websocket.CloseNormalClosure) {
					// 	log.Fatalln("Disconnected from uyghurs server,", err)
					// } else {
					// 	log.Fatalln("Disconnected from uyghurs server, unknown err,", err)
					// }
				}

				err = json.Unmarshal(messageBytes, &projectsMetadataMessage)

				if err != nil {
					log.Println("read error:", err)

					continue
				}

				log.Println("Updating projects routes...")

				for _, projectMetadata := range projectsMetadataMessage {
					log.Println("Updating...", projectMetadata.ProjectName)

					routesManager.UpdateProjectRoutes(projectMetadata)
				}
			}
		}()
	}

	r := gin.Default()

	r.NoRoute(func(c *gin.Context) {
		slashIndex := strings.Index(c.Request.URL.Path[1:], "/")

		pathPrefix := c.Request.URL.Path

		if slashIndex != -1 {
			pathPrefix = pathPrefix[:slashIndex+1]
		}

		routeInfo, exists := routesManager.GetRouteInfo(c.Request.Host, pathPrefix)

		if !exists {
			routeInfo = routesManager.GetDefaultRouteInfo()
		}

		routeInfo.ReverseProxyHandler(c)
	})

	if *development {
		r.Run(":9900")
	} else {
		r.RunTLS(":443", "./secrets/cloudflare.crt", "./secrets/cloudflare.secret")
	}
}
