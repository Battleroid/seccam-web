package main

import (
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/julienschmidt/httprouter"
	_ "github.com/mattn/go-sqlite3"
)

type dirs struct {
	videos string
	images string
}

type Config struct {
	db   string
	addr string
	dirs
}

type App struct {
	DB     *sql.DB
	Config *Config
	Router *httprouter.Router
}

type Event struct {
	Id    int64
	Name  string
	Time  time.Time
	Video string
	Image string
}

type Events []Event

func InitDB(path string) *sql.DB {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		panic(err)
	}
	if db == nil {
		panic("DB nil")
	}
	return db
}

func CreateTable(db *sql.DB) {
	sql := `
	CREATE TABLE IF NOT EXISTS events(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		time TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		video TEXT NOT NULL,
		image TEXT NOT NULL
	)`
	_, err := db.Exec(sql)
	if err != nil {
		panic(err)
	}
}

func New(config *Config) *App {
	// Create database, tables and our router
	db := InitDB(config.db)
	CreateTable(db)
	router := httprouter.New()

	// Create paths for storing videos and images
	for _, path := range []string{config.dirs.videos, config.dirs.images} {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			os.Mkdir(path, 0775)
		}
	}

	// Create App struct
	app := &App{
		DB:     db,
		Config: config,
		Router: router,
	}

	return app
}

func (app *App) CreateEvent(event Event) {
	sql := `
	INSERT INTO events(
		name,
		video,
		image
	) VALUES (?, ?, ?)`
	stmt, err := app.DB.Prepare(sql)
	if err != nil {
		panic(err)
	}
	defer stmt.Close()

	_, err2 := stmt.Exec(event.Name, event.Video, event.Image)
	if err2 != nil {
		panic(err)
	}
}

func (app *App) NewEventHandler(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	// Parse form
	var err error
	r.ParseMultipartForm(104857600) // 100 MB

	// Get video & image files
	videoFile, vHandler, err := r.FormFile("video")
	imageFile, iHandler, err := r.FormFile("image")
	if err != nil {
		panic(err)
	}

	// Save files
	vPath := filepath.Join(app.Config.dirs.videos, vHandler.Filename)
	iPath := filepath.Join(app.Config.dirs.images, iHandler.Filename)

	vDest, err := os.OpenFile(vPath, os.O_WRONLY|os.O_CREATE, 0775)
	iDest, err := os.OpenFile(iPath, os.O_WRONLY|os.O_CREATE, 0775)
	if err != nil {
		panic(err)
	}

	// Defer closing form and destination files
	defer videoFile.Close()
	defer imageFile.Close()
	defer vDest.Close()
	defer iDest.Close()

	// Copy contents from form file to destination
	io.Copy(vDest, videoFile)
	io.Copy(iDest, imageFile)

	// Create event information
	event := Event{
		Name:  r.FormValue("name"),
		Image: vPath,
		Video: iPath,
	}

	// Create new event if fields are not null
	if event.Name != "" && event.Image != "" && event.Video != "" {
		w.WriteHeader(http.StatusAccepted)
		app.CreateEvent(event)
		// TODO: event should sent text message as well
		return
	}

	// Something was null, return unacceptable
	w.WriteHeader(http.StatusNotAcceptable)
}

func (app *App) IndexHandler(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	// TODO: limit should be set in config
	sql := `SELECT * FROM events ORDER BY id DESC LIMIT 5`
	rows, err := app.DB.Query(sql)
	if err != nil {
		panic(err)
	}
	defer rows.Close()

	events := make([]*Event, 0)
	for rows.Next() {
		event := new(Event)
		err := rows.Scan(
			&event.Id,
			&event.Name,
			&event.Time,
			&event.Video,
			&event.Image,
		)
		if err != nil {
			panic(err)
		}
		events = append(events, event)
	}
	if err = rows.Err(); err != nil {
		panic(err)
	}

	for _, event := range events {
		fmt.Fprintf(w, "%d %s %s %s %s\n", event.Id, event.Name, event.Time, event.Video, event.Image)
	}
}

func main() {
	config := Config{}

	// Set config values based off CLI params (or defaults)
	flag.StringVar(&config.db, "db", "./events.db", "Database filename")
	flag.StringVar(&config.dirs.videos, "video", "./videos", "Video directory")
	flag.StringVar(&config.dirs.images, "image", "./images", "Image directory")
	flag.StringVar(&config.addr, "address", ":8080", "Address and port to listen on")
	flag.Parse()

	// Create application with our config
	app := New(&config)

	// Our few routes
	app.Router.GET("/", app.IndexHandler)
	app.Router.POST("/event", app.NewEventHandler)

	// Start HTTP server
	log.Println("Starting")
	log.Fatal(http.ListenAndServe(config.addr, app.Router))
}
