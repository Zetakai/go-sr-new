package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

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

var (
	db                  *sql.DB
	apiKey              string
	lastRecommendations []string // Store the last recommended video IDs to avoid repetition
)

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
	r.HandleFunc("/recommendation", getRecommendedVideo).Methods("GET")

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
	if err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	// Check if this is a direct URL addition (from recommendation)
	if youtubeURL.URL != "" && youtubeURL.Title != "" && youtubeURL.User != "" {
		// Just insert the provided URL directly
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
		return
	}

	// Otherwise, this is a title-based addition
	if youtubeURL.Title == "" || youtubeURL.User == "" {
		http.Error(w, "Invalid request payload - missing title or user", http.StatusBadRequest)
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

// Updated recommendation function with more variety
func getRecommendedVideo(w http.ResponseWriter, r *http.Request) {
	// Get the most recently played video ID (if any)
	var lastVideo YouTubeURL
	err := db.QueryRow("SELECT id, title, url, user FROM youtube_urls ORDER BY id DESC LIMIT 1").Scan(&lastVideo.ID, &lastVideo.Title, &lastVideo.URL, &lastVideo.User)

	// Default search queries for variety when no specific relatedness is available
	searchQueries := []string{
		"popular music",
		"trending songs",
		"top hits",
		"indie music",
		"recommended playlist",
		"viral songs",
		"new music releases",
		"best music today",
	}

	// Randomly select a search query if we need to use one
	rand.Seed(time.Now().UnixNano())
	searchQuery := searchQueries[rand.Intn(len(searchQueries))]

	// Number of results to request (we'll pick one randomly)
	maxResults := "10"

	client := resty.New()
	baseURL := "https://www.googleapis.com/youtube/v3/search"
	params := url.Values{}
	params.Set("part", "snippet")
	params.Set("type", "video")
	params.Set("videoCategoryId", "10") // 10 is for Music category
	params.Set("maxResults", maxResults)
	params.Set("key", apiKey)

	// If we have a previous video, try to get related content
	if err == nil && lastVideo.URL != "" {
		videoID := extractVideoID(lastVideo.URL)
		if videoID != "" {
			params.Set("relatedToVideoId", videoID)

			resp, err := client.R().
				SetQueryString(params.Encode()).
				Get(baseURL)

			if err == nil && resp.StatusCode() == 200 {
				var result map[string]interface{}
				if err := json.Unmarshal(resp.Body(), &result); err == nil {
					items, ok := result["items"].([]interface{})
					if ok && len(items) > 0 {
						// We have results, pick a random one that hasn't been recommended recently
						recommendation := getRandomRecommendation(items)
						w.Header().Set("Content-Type", "application/json")
						json.NewEncoder(w).Encode(recommendation)
						return
					}
				}
			}
		}
	}

	// If relation-based search failed or no previous video exists,
	// search by a random query term
	params.Del("relatedToVideoId") // Remove the related parameter
	params.Set("q", searchQuery)

	resp, err := client.R().
		SetQueryString(params.Encode()).
		Get(baseURL)

	if err != nil || resp.StatusCode() != 200 {
		http.Error(w, "Error finding recommendation", http.StatusInternalServerError)
		return
	}

	var result map[string]interface{}
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		http.Error(w, "Error parsing recommendation results", http.StatusInternalServerError)
		return
	}

	items, ok := result["items"].([]interface{})
	if !ok || len(items) == 0 {
		http.Error(w, "No recommendations found", http.StatusNotFound)
		return
	}

	// Get a random recommendation
	recommendation := getRandomRecommendation(items)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(recommendation)
}

// Helper function to select a random recommendation from search results
func getRandomRecommendation(items []interface{}) YouTubeURL {
	// Max number of IDs to store in lastRecommendations
	const maxLastRecommendations = 20

	// Collect all valid items that haven't been recommended recently
	var validItems []interface{}

	for _, item := range items {
		// Extract the video ID
		videoID, ok := item.(map[string]interface{})["id"].(map[string]interface{})["videoId"].(string)
		if !ok || videoID == "" {
			continue
		}

		// Check if this video was recently recommended
		alreadyRecommended := false
		for _, lastID := range lastRecommendations {
			if lastID == videoID {
				alreadyRecommended = true
				break
			}
		}

		if !alreadyRecommended {
			validItems = append(validItems, item)
		}
	}

	// If all results were recently recommended, use all items
	if len(validItems) == 0 {
		validItems = items
	}

	// Select a random item
	randomIndex := rand.Intn(len(validItems))
	selectedItem := validItems[randomIndex]

	snippet := selectedItem.(map[string]interface{})["snippet"].(map[string]interface{})
	videoID := selectedItem.(map[string]interface{})["id"].(map[string]interface{})["videoId"].(string)

	title := snippet["title"].(string)
	videoURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)

	// Add this video ID to lastRecommendations
	lastRecommendations = append(lastRecommendations, videoID)

	// Keep lastRecommendations at a reasonable size
	if len(lastRecommendations) > maxLastRecommendations {
		// Remove the oldest recommendations
		lastRecommendations = lastRecommendations[len(lastRecommendations)-maxLastRecommendations:]
	}

	return YouTubeURL{
		Title: title,
		URL:   videoURL,
		User:  "Recommended",
	}
}

// Helper function to extract YouTube video ID from URL
func extractVideoID(url string) string {
	// Simple extraction, can be improved with regex for robustness
	if strings.Contains(url, "v=") {
		parts := strings.Split(url, "v=")
		if len(parts) > 1 {
			idParts := strings.Split(parts[1], "&")
			return idParts[0]
		}
	} else if strings.Contains(url, "youtu.be/") {
		parts := strings.Split(url, "youtu.be/")
		if len(parts) > 1 {
			return parts[1]
		}
	}
	return ""
}
