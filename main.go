package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"

	"github.com/gorilla/mux"
	_ "modernc.org/sqlite"
)

type YouTubeURL struct {
	ID  int    `json:"id"`
	URL string `json:"url"`
}

var db *sql.DB

func main() {
	// Initialize database
	initDB()

	// Initialize router
	r := mux.NewRouter()

	// Define routes
	r.HandleFunc("/", requesterHandler).Methods("GET")
	r.HandleFunc("/host", hostHandler).Methods("GET")
	r.HandleFunc("/url", addURL).Methods("POST")
	r.HandleFunc("/url", deleteURL).Methods("DELETE") // Delete by URL
	r.HandleFunc("/url/oldest", getOldestURLAndDelete).Methods("GET")
	r.HandleFunc("/urls", getAllURLs).Methods("GET")

	// Serve the static HTML file and other assets like CSS/JS files
	staticDir := http.Dir("./")
	staticFileServer := http.FileServer(staticDir)

	r.PathPrefix("/").Handler(http.StripPrefix("/", staticFileServer))

	// Start server
	log.Println("Starting server on http://localhost:420/")
	log.Fatal(http.ListenAndServe(":420", r))
}

func initDB() {
	var err error
	// Open SQLite database
	db, err = sql.Open("sqlite", "./youtube_urls.db")
	if err != nil {
		log.Fatal(err)
	}

	// Create table if it doesn't exist
	createTable := `
	CREATE TABLE IF NOT EXISTS youtube_urls (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		url TEXT NOT NULL UNIQUE
	);`
	_, err = db.Exec(createTable)
	if err != nil {
		log.Fatal(err)
	}
}

// Handler to render the host page
func hostHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "host.html")
}

func requesterHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "requester.html")
}

func addURL(w http.ResponseWriter, r *http.Request) {
	var youtubeURL YouTubeURL
	err := json.NewDecoder(r.Body).Decode(&youtubeURL)
	if err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	// Validate the URL
	if !isValidYouTubeURL(youtubeURL.URL) {
		http.Error(w, "Invalid YouTube URL", http.StatusBadRequest)
		return
	}

	// Insert URL into the database if it doesn't already exist
	stmt, err := db.Prepare("INSERT INTO youtube_urls (url) VALUES (?)")
	if err != nil {
		http.Error(w, "Error preparing query", http.StatusInternalServerError)
		return
	}
	defer stmt.Close()

	_, err = stmt.Exec(youtubeURL.URL)
	if err != nil {
		http.Error(w, "URL already exists or error inserting URL", http.StatusConflict)
		return
	}

	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, "URL added successfully")
}

func deleteURL(w http.ResponseWriter, r *http.Request) {
	var youtubeURL YouTubeURL
	err := json.NewDecoder(r.Body).Decode(&youtubeURL)
	if err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	// Delete URL by matching the exact URL
	stmt, err := db.Prepare("DELETE FROM youtube_urls WHERE url = ?")
	if err != nil {
		http.Error(w, "Error preparing query", http.StatusInternalServerError)
		return
	}
	defer stmt.Close()

	res, err := stmt.Exec(youtubeURL.URL)
	if err != nil {
		http.Error(w, "Error deleting URL", http.StatusInternalServerError)
		return
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		http.Error(w, "URL not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "URL deleted successfully")
}

func getOldestURLAndDelete(w http.ResponseWriter, r *http.Request) {
	var youtubeURL YouTubeURL

	// Select the oldest URL (the one with the smallest ID)
	err := db.QueryRow("SELECT id, url FROM youtube_urls ORDER BY id ASC LIMIT 1").Scan(&youtubeURL.ID, &youtubeURL.URL)
	if err != nil {
		http.Error(w, "No URL found", http.StatusNotFound)
		return
	}

	// Delete the oldest URL after retrieving it
	err = deleteURLByID(youtubeURL.ID)
	if err != nil {
		http.Error(w, "Error deleting URL", http.StatusInternalServerError)
		return
	}

	// Return the oldest URL
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(youtubeURL)
}

// Return all URLs in an array
func getAllURLs(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query("SELECT id, url FROM youtube_urls")
	if err != nil {
		http.Error(w, "Error fetching URLs", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var urls []YouTubeURL

	for rows.Next() {
		var youtubeURL YouTubeURL
		err := rows.Scan(&youtubeURL.ID, &youtubeURL.URL)
		if err != nil {
			http.Error(w, "Error scanning URL", http.StatusInternalServerError)
			return
		}
		urls = append(urls, youtubeURL)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(urls)
}

// Validates if the URL is a valid YouTube URL
func isValidYouTubeURL(url string) bool {
	// Simple regex to match YouTube URLs
	regex := `^(https?://)?(www\.)?(youtube\.com|youtu\.be)/.+$`
	re := regexp.MustCompile(regex)
	return re.MatchString(url)
}

func deleteURLByID(id int) error {
	stmt, err := db.Prepare("DELETE FROM youtube_urls WHERE id = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(id)
	return err
}
