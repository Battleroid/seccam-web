package main

import (
	"database/sql"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/julienschmidt/httprouter"
	_ "github.com/mattn/go-sqlite3"
	"github.com/sfreiberg/gotwilio"
)

// Data directories struct
type dirs struct {
	data string
	tmpl string
}

// Twilio information struct
type twilio struct {
	sid   string
	token string
	from  string
	to    string
}

// Configuration information struct
type Config struct {
	db   string
	addr string
	twilio
	dirs
}

// Application context struct
type App struct {
	DB        *sql.DB
	Config    *Config
	Router    *httprouter.Router
	Templates map[string]*template.Template
}

// Event information struct
type Event struct {
	Id    int64
	Name  string
	Time  time.Time
	Video string
	Image string
}

// Initialize our SQLite database.
func InitDB(path string) *sql.DB {
	// Attempt to open the database
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		panic(err)
	}

	// The database isn't nil?
	if db == nil {
		panic("DB nil")
	}

	// Can we reach the database?
	err = db.Ping()
	if err != nil {
		panic(err)
	}

	return db
}

// Create our table in our database.
func CreateTable(db *sql.DB) {
	// Create table SQL statement
	sql_table := `
	CREATE TABLE IF NOT EXISTS events(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		time TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		video TEXT NOT NULL,
		image TEXT NOT NULL
	)`

	// Execute statement
	_, err := db.Exec(sql_table)
	if err != nil {
		panic(err)
	}
}

// Creates a new Application context. The context contains configuration information,
// templating info, our router, and database access. Creation of the data directory is
// also performed here.
func New(config *Config) *App {
	// Create database, tables, templates map and our router
	db := InitDB(config.db)
	CreateTable(db)
	router := httprouter.New()

	// Build our [sparse] map of templates
	templates := map[string]*template.Template{}
	templates["index"] = template.Must(template.ParseFiles(filepath.Join(config.dirs.tmpl, "index.html")))

	// Create path for storing videos and images
	if _, err := os.Stat(config.dirs.data); os.IsNotExist(err) {
		os.Mkdir(config.dirs.data, 0775)
	}

	// Create App struct
	app := &App{
		DB:        db,
		Config:    config,
		Router:    router,
		Templates: templates,
	}

	return app
}

// Retrieves a single event with the given Id.
func (app *App) GetEvent(id int64) Event {
	var err error

	// Query for row id
	sql_row := `SELECT * FROM events WHERE id = ?`
	row := app.DB.QueryRow(sql_row, id)

	// Get event info
	event := Event{}
	err = row.Scan(
		&event.Id,
		&event.Name,
		&event.Time,
		&event.Video,
		&event.Image,
	)
	if err == sql.ErrNoRows {
		panic(err)
	} else if err != nil {
		panic(err)
	}

	return event
}

// Creates a new event with the given information.
func (app *App) CreateEvent(event Event) int64 {
	var err error

	// Prepare SQL statement
	sql_event := `
	INSERT INTO events(
		name,
		video,
		image
	) VALUES (?, ?, ?)`
	stmt, err := app.DB.Prepare(sql_event)
	if err != nil {
		panic(err)
	}
	defer stmt.Close()

	// Execute statement
	res, err := stmt.Exec(event.Name, event.Video, event.Image)
	if err != nil {
		panic(err)
	}

	// Get the newly created row id from our last insert
	rowId, err := res.LastInsertId()
	if err != nil {
		panic(err)
	}

	log.Println("Created new event", event.Name)

	return rowId
}

// Accepts POST data and creates a new event if the information is acceptable.
// Will also use ffmpeg (if installed) to convert the video to a more browser
// friendly container.
func (app *App) NewEventHandler(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	var err error

	// Parse form
	r.ParseMultipartForm(104857600) // 100 MB
	name := r.FormValue("name")

	// Get video & image files
	videoFile, vHandler, err := r.FormFile("video")
	imageFile, iHandler, err := r.FormFile("image")
	if err != nil {
		panic(err)
	}

	// Create path for new files
	vPath := filepath.Join(app.Config.dirs.data, vHandler.Filename)
	iPath := filepath.Join(app.Config.dirs.data, iHandler.Filename)

	// Create new file
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

	// Re-encode video to something friendly for browsers
	newVideoPath := strings.TrimSuffix(vPath, filepath.Ext(vPath)) + ".mp4"
	cmd := exec.Command("ffmpeg", "-i", vPath, "-c:v", "libx264", "-crf", "21", "-vf", "scale=w=320:h=240", "-y", newVideoPath)

	// Remove old video (avi) and set new path if successful
	if err := cmd.Run(); err == nil {
		os.Remove(vPath)
		vPath = newVideoPath
	} else {
		log.Printf("Error converting %s to %s\n", vPath, newVideoPath)
		log.Println(err.Error())
	}

	// Create event information
	event := Event{
		Name:  name,
		Image: iPath,
		Video: vPath,
	}

	// Create new event if fields are not null
	if event.Name != "" && event.Image != "" && event.Video != "" {
		rowId := app.CreateEvent(event)
		event := app.GetEvent(rowId)
		app.SendSMS(&event)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Something was null, return unacceptable
	w.WriteHeader(http.StatusNotAcceptable)
}

// Renders the index of events
func (app *App) IndexHandler(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	// Prepare SQL query
	sql_index := `SELECT * FROM events ORDER BY id DESC LIMIT 5`
	rows, err := app.DB.Query(sql_index)
	if err != nil {
		panic(err)
	}
	defer rows.Close()

	// Build array of events
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

	// Render template with given events for context
	t := app.Templates["index"]
	t.ExecuteTemplate(w, t.Name(), events)
}

// Sends an SMS with the relevant Event information, primitive at the moment
func (app *App) SendSMS(event *Event) {
	twilio := gotwilio.NewTwilioClient(app.Config.sid, app.Config.token)
	message := fmt.Sprintf("Motion event captured at %s.", event.Time)
	_, _, err := twilio.SendSMS(app.Config.twilio.from, app.Config.twilio.to, message, "", "") // TODO: change to MMS
	if err != nil {
		log.Printf("Error sending SMS to %s\n", app.Config.twilio.to)
	}
}

func main() {
	config := Config{}

	// Set config values based off CLI params (or defaults)
	flag.StringVar(&config.db, "db", "./events.db", "Database filename")
	flag.StringVar(&config.dirs.data, "data", "./data", "Data directory")
	flag.StringVar(&config.addr, "address", ":8000", "Address and port to listen on")
	flag.StringVar(&config.twilio.sid, "sid", "", "Twilio SID")
	flag.StringVar(&config.twilio.token, "token", "", "Twilio auth token")
	flag.StringVar(&config.twilio.from, "from", "", "From number")
	flag.StringVar(&config.twilio.to, "to", "", "To number")
	flag.StringVar(&config.dirs.tmpl, "tmpl", "tmpl", "Template directory")
	flag.Parse()

	// Create application with our config
	app := New(&config)

	// Our few routes
	app.Router.GET("/", app.IndexHandler)
	app.Router.POST("/event/new", app.NewEventHandler)

	// Handler for serving files in case we are not behind something else such as nginx
	app.Router.ServeFiles("/data/*filepath", http.Dir(app.Config.dirs.data))

	// Start HTTP server
	log.Println("Starting")
	log.Fatal(http.ListenAndServe(config.addr, app.Router))
}
