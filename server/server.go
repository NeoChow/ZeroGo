package server

import (
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"path"
	"time"

	"github.com/G1itchZero/ZeroGo/socket"
	"github.com/G1itchZero/ZeroGo/utils"

	"github.com/G1itchZero/ZeroGo/site_manager"
	log "github.com/Sirupsen/logrus"
	"github.com/fatih/color"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo"
	"gopkg.in/cheggaaa/pb.v1"
)

type Server struct {
	Port    int
	Sockets map[string]*socket.UiSocket
	Sites   *site_manager.SiteManager
	pbPool  *pb.Pool
}

func NewServer(port int, sites *site_manager.SiteManager) *Server {
	pool, _ := pb.StartPool()
	server := Server{
		Port:    port,
		Sockets: map[string]*socket.UiSocket{},
		Sites:   sites,
		pbPool: pool,
	}
	return &server
}

func NoCacheMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		header := ctx.Response().Header()
		header.Set("Cache-Control", "no-cache, private, max-age=0, no-store, must-revalidate")
		header.Set("Expires", time.Unix(0, 0).Format(http.TimeFormat))
		header.Set("Pragma", "no-cache")
		header.Set("X-Accel-Expires", "0")
		return next(ctx)
	}
}

func InnerMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		if ctx.Path() == "/inner/:site" {
			site := ctx.Param("site")
			cookie := new(http.Cookie)
			cookie.Name = "site"
			cookie.Value = site
			cookie.Expires = time.Now().Add(24 * time.Hour)
			ctx.SetCookie(cookie)
		}
		return next(ctx)
	}
}

var (
	upgrader = websocket.Upgrader{}
)

func (s *Server) socketHandler(c echo.Context) error {
	ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	wrapperKey := c.QueryParam("wrapper_key")
	if err != nil {
		log.WithFields(log.Fields{
			"wrapperKey": wrapperKey,
			"err":        err,
		}).Error("Socket upgrade error")
		return err
	}
	defer ws.Close()

	socket, ok := s.Sockets[wrapperKey]

	if ws != nil && ok {
		socket.Serve(ws)
	}
	return errors.New("Socket closed")
}

func (s *Server) Serve() {
	e := echo.New()
	e.Use(NoCacheMiddleware)
	e.Static("/uimedia", path.Join(utils.GetDataPath(), utils.ZN_UPDATE, utils.ZN_MEDIA))

	inner := e.Group("/inner")
	inner.Use(InnerMiddleware)
	inner.Use(NoCacheMiddleware)
	inner.GET("/:site", s.serveInner)
	inner.GET("/*", s.serveInnerStatic)

	e.GET("/Websocket", s.socketHandler)
	e.GET("/:url/", s.serveWrapper)
	e.GET("/:url", s.serveWrapper)
	e.GET("/", s.serveWrapper)

	e.Logger.Fatal(fmt.Errorf("Serving result: %v", e.Start(fmt.Sprintf(":%d", s.Port))))
}

func (s *Server) serveWrapper(ctx echo.Context) error {
	yellow := color.New(color.FgYellow).SprintFunc()
	url := ctx.Param("url")
	if url == "" {
		url = utils.GetHomepage()
	}
	if url == "favicon.ico" {
		return nil
	}
	st := s.Sites.Get(url)
	s.pbPool.Add(st.Downloader.ProgressBar)
	if st == nil {
		ctx.HTML(404, "No .bit name found")
		return errors.New("No .bit name found")
	}
	log.Info(fmt.Sprintf("> %s", yellow(st.Address)))
	s.Sites.Sites[url] = st
	s.Sites.Sites[st.Address] = st
	wrapper := NewWrapper(st)
	err := wrapper.Render(ctx)
	if err != nil {
		log.Error("Wrapper rendering error", err)
	}
	socket := socket.NewUiSocket(st, s.Sites, wrapper.Key)
	s.Sockets[wrapper.Key] = socket
	return err
}

func (s *Server) serveInner(ctx echo.Context) error {
	name := ctx.Param("site")
	root := path.Join(utils.GetDataPath(), name)
	filename := path.Join(root, "index.html")
	site := s.Sites.Sites[name]
	site.WaitFile("index.html")
	return ctx.File(filename)
}

func (s *Server) serveInnerStatic(ctx echo.Context) error {
	name, _ := ctx.Cookie("site")
	url := ctx.Param("*")
	root := path.Join(utils.GetDataPath(), name.Value)
	filename := path.Join(root, url)
	site := s.Sites.Sites[name.Value]
	site.WaitFile(url)
	return ctx.File(filename)
}

type Template struct {
	templates *template.Template
}

func (t *Template) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	return t.templates.ExecuteTemplate(w, name, data)
}
