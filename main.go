package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"

	"github.com/go-resty/resty/v2"
	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
	_ "modernc.org/sqlite"
)

type YouTubeURL struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
	User  string `json:"user"`
}

var db *sql.DB
var apiKey string

func main() {
	// Load environment variables from .env
	if err := godotenv.Load(); err != nil {
		log.Fatal("Error loading .env file")
	}

	// Ensure the API key is available
	apiKey = os.Getenv("YOUTUBE_API_KEY")
	if apiKey == "" {
		log.Fatal("YOUTUBE_API_KEY not found in environment")
	}

	initDB()

	// Initialize the router
	r := mux.NewRouter()
	r.HandleFunc("/", requesterHandler).Methods("GET")
	r.HandleFunc("/host", hostHandler).Methods("GET")
	r.HandleFunc("/url", addURL).Methods("POST")
	r.HandleFunc("/url", deleteURL).Methods("DELETE")
	r.HandleFunc("/url/oldest", getOldestURLAndDelete).Methods("GET")
	r.HandleFunc("/urls", getAllURLs).Methods("GET")

	// Serve static files
	staticDir := http.Dir("./")
	r.PathPrefix("/").Handler(http.FileServer(staticDir))

	log.Println("Starting server on http://localhost:420/")
	log.Fatal(http.ListenAndServe(":420", r))
}

func initDB() {
	var err error
	db, err = sql.Open("sqlite", "./youtube_urls.db")
	if err != nil {
		log.Fatal(err)
	}

	createTable := `
	CREATE TABLE IF NOT EXISTS youtube_urls (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		url TEXT NOT NULL UNIQUE,
		user TEXT NOT NULL
	);`
	_, err = db.Exec(createTable)
	if err != nil {
		log.Fatal(err)
	}
}

func requesterHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "requester.html")
}

func hostHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "host.html")
}

func addURL(w http.ResponseWriter, r *http.Request) {
	var youtubeURL YouTubeURL
	err := json.NewDecoder(r.Body).Decode(&youtubeURL)
	if err != nil || youtubeURL.Title == "" || youtubeURL.User == "" {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	actualTitle, matchedURL, err := findClosestYouTubeMatch(youtubeURL.Title)
	if err != nil {
		http.Error(w, "Error finding YouTube match", http.StatusInternalServerError)
		return
	}

	youtubeURL.Title = actualTitle
	youtubeURL.URL = matchedURL

	stmt, err := db.Prepare("INSERT INTO youtube_urls (title, url, user) VALUES (?, ?, ?)")
	if err != nil {
		http.Error(w, "Error preparing query", http.StatusInternalServerError)
		return
	}
	defer stmt.Close()

	_, err = stmt.Exec(youtubeURL.Title, youtubeURL.URL, youtubeURL.User)
	if err != nil {
		http.Error(w, "URL already exists or error inserting URL", http.StatusConflict)
		return
	}

	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, "Song added successfully: %s by %s", youtubeURL.Title, youtubeURL.User)
}

func deleteURL(w http.ResponseWriter, r *http.Request) {
	var youtubeURL YouTubeURL
	err := json.NewDecoder(r.Body).Decode(&youtubeURL)
	if err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

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
	err := db.QueryRow("SELECT id, title, url, user FROM youtube_urls ORDER BY id ASC LIMIT 1").Scan(&youtubeURL.ID, &youtubeURL.Title, &youtubeURL.URL, &youtubeURL.User)
	if err != nil {
		http.Error(w, "No URL found", http.StatusNotFound)
		return
	}

	err = deleteURLByID(youtubeURL.ID)
	if err != nil {
		http.Error(w, "Error deleting URL", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(youtubeURL)
}

func getAllURLs(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query("SELECT id, title, url, user FROM youtube_urls")
	if err != nil {
		http.Error(w, "Error fetching URLs", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var urls []YouTubeURL
	for rows.Next() {
		var youtubeURL YouTubeURL
		err := rows.Scan(&youtubeURL.ID, &youtubeURL.Title, &youtubeURL.URL, &youtubeURL.User)
		if err != nil {
			http.Error(w, "Error scanning URL", http.StatusInternalServerError)
			return
		}
		urls = append(urls, youtubeURL)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(urls)
}

func findClosestYouTubeMatch(query string) (string, string, error) {
	client := resty.New()

	baseURL := "https://www.googleapis.com/youtube/v3/search"
	params := url.Values{}
	params.Set("part", "snippet")
	params.Set("q", query)
	params.Set("type", "video")
	params.Set("maxResults", "1")
	params.Set("key", apiKey)

	resp, err := client.R().
		SetQueryString(params.Encode()).
		Get(baseURL)

	if err != nil {
		return "", "", fmt.Errorf("failed to perform request: %w", err)
	}

	if resp.StatusCode() != 200 {
		return "", "", fmt.Errorf("unexpected status code: %d", resp.StatusCode())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return "", "", fmt.Errorf("failed to parse JSON: %w", err)
	}

	items, ok := result["items"].([]interface{})
	if !ok || len(items) == 0 {
		return "", "", fmt.Errorf("no matching videos found")
	}

	snippet := items[0].(map[string]interface{})["snippet"].(map[string]interface{})
	videoID := items[0].(map[string]interface{})["id"].(map[string]interface{})["videoId"].(string)

	title := snippet["title"].(string)
	videoURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)

	log.Printf("Found video: %s (%s)", title, videoURL)
	return title, videoURL, nil
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
