package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
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
	"github.com/joho/godotenv"
	"github.com/the-rileyj/uyghurs"
)

type domainRoutesManager struct {
	routesMap    map[string]*extendedRouteInfo
	domainRegexp *regexp.Regexp
}

type routesManager struct {
	defaultDomain   string
	domainRoutesMap map[string]*domainRoutesManager
	projectsMap     map[string]*uyghurs.ProjectMetadata
	lock            *sync.Mutex
}

type extendedRouteInfo struct {
	uyghurs.RouteInfo
	ReverseProxyHandler gin.HandlerFunc
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
		routesMap: map[string]*extendedRouteInfo{
			"/": {
				RouteInfo: uyghurs.RouteInfo{
					Domain:      defaultDomain,
					ForwardHost: defaultHost,
					Route:       "/",
				},
				ReverseProxyHandler: func(c *gin.Context) { defaultDomainReverseProxy.ServeHTTP(c.Writer, c.Request) },
			},
		},
		domainRegexp: nil,
	}

	return rM
}

func (rM *routesManager) GetDefaultRouteInfo() *extendedRouteInfo {
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

func (rM *routesManager) GetRouteInfo(domain, route string) (*extendedRouteInfo, bool) {
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

			if !exists {
				// Fallback to default route for domain
				routeInfo, exists = domainManager.routesMap["/"]
			}

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

			domainRoutesManager, domainRoutesManagerExists := rM.domainRoutesMap[domain]

			if domainRoutesManagerExists {
				delete(domainRoutesManager.routesMap, routeInfo.Route)

				if len(domainRoutesManager.routesMap) == 0 {
					delete(rM.domainRoutesMap, domain)
				}
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
				routesMap:    make(map[string]*extendedRouteInfo),
				domainRegexp: domainRegexp,
			}
		}

		newRouteHostURL, err := url.Parse(routeInfo.ForwardHost)

		if err != nil {
			log.Printf("Failed to add new route %s: %s\n", fmt.Sprintf(routeInfo.Domain+routeInfo.Route), err)

			continue
		}

		newRouteReverseProxy := httputil.NewSingleHostReverseProxy(newRouteHostURL)

		domainRoutesMan.routesMap[routeInfo.Route] = &extendedRouteInfo{
			RouteInfo:           *routeInfo,
			ReverseProxyHandler: func(c *gin.Context) { newRouteReverseProxy.ServeHTTP(c.Writer, c.Request) },
		}

		rM.domainRoutesMap[routeInfo.Domain] = domainRoutesMan
	}

	rM.projectsMap[projectMetadata.ProjectName] = projectMetadata
}

func main() {
	development := flag.Bool("d", false, "development flag")

	defaultDomain := flag.String("dd", "therileyjohnson.com", "the most frequently used domain, roughly the default")
	defaultHost := flag.String("dh", "http://rj-site", "the default host to forward requests to")

	envFile := flag.Bool("env", false, "use env file for config")

	flag.Parse()

	if *envFile {
		err := godotenv.Load()

		if err != nil {
			log.Fatal("Error loading .env file")
		}
	}

	envVars := make(map[string]string)

	// for _, envVarKey := range []string{"DEVELOPMENT", "UYGHURS_CONNECTION_HOST", "UYGHURS_CONNECTION_SECRET", "UYGHURS_CONNECTION_SCHEME"} {
	for _, envVarKey := range []string{"UYGHURS_CONNECTION_HOST", "UYGHURS_CONNECTION_SECRET", "UYGHURS_CONNECTION_SCHEME"} {
		envVarValue := os.Getenv(envVarKey)

		if envVarValue == "" {
			log.Fatalf(`environmental variable "%s" is not set`, envVarKey)
		}

		// Assure no extra whitespace characters (issue on windows with \r\n endings)
		envVars[envVarKey] = strings.Trim(envVarValue, "\r\n")
	}

	// if !*development {
	// 	*development = envVars["DEVELOPMENT"] != ""
	// }

	uyghursConnectionHost := envVars["UYGHURS_CONNECTION_HOST"]
	uyghursConnectionSecret := envVars["UYGHURS_CONNECTION_SECRET"]
	uyghursConnectionScheme := envVars["UYGHURS_CONNECTION_SCHEME"]

	routesManager := newRoutesManager(*defaultDomain, *defaultHost)

	go func() {
		uyghursURL := url.URL{Scheme: uyghursConnectionScheme, Host: uyghursConnectionHost, Path: fmt.Sprintf("/router/%s", uyghursConnectionSecret)}

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
				if uyghursConnection != nil {
					uyghursConnection.Close()
				}

				uyghursConnection = connectIndefinitely()

				log.Println("Reconnected to uyghurs server!")

				continue
			}

			err = json.Unmarshal(messageBytes, &projectsMetadataMessage)

			if err != nil {
				log.Println("read error:", err)

				continue
			}

			for _, projectMetadata := range projectsMetadataMessage {
				log.Printf("Updating %s...", projectMetadata.ProjectName)

				for _, projectRoute := range projectMetadata.ProjectRoutes {
					log.Printf("\"%s%s\" -> \"%s%s\" \n", projectRoute.Domain, projectRoute.Route, projectRoute.ForwardHost, projectRoute.Route)
				}

				routesManager.UpdateProjectRoutes(projectMetadata)
			}
		}
	}()

	r := gin.Default()

	r.GET("/routing", func(c *gin.Context) {
		routesManager.lock.Lock()
		defer routesManager.lock.Unlock()

		simplifiedRoutingMap := make(map[string]map[string]string)

		for _, domainManager := range routesManager.domainRoutesMap {
			if domainManager.domainRegexp == nil {
				continue
			}

			simplifiedForwardingMap := make(map[string]string)

			for domainRoute, domainRouteExtendedInfo := range domainManager.routesMap {
				simplifiedForwardingMap[domainRoute] = domainRouteExtendedInfo.Route
			}

			simplifiedRoutingMap[domainManager.domainRegexp.String()] = simplifiedForwardingMap
		}

		simplifiedRoutingMapJSONBytes, err := json.MarshalIndent(simplifiedRoutingMap, "", "\t")

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"msg":  "err wrangling routes",
				"err":  true,
				"data": gin.H{},
			})

			return
		}

		c.Writer.Write(simplifiedRoutingMapJSONBytes)
	})

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
