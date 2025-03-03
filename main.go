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
	"regexp"
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

	// Create a new table to track recommended videos
	createRecommendationsTable := `
	CREATE TABLE IF NOT EXISTS recommended_videos (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		video_id TEXT NOT NULL UNIQUE,
		timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`
	_, err = db.Exec(createRecommendationsTable)
	if err != nil {
		log.Fatal(err)
	}

	// Add index for faster queries
	_, err = db.Exec("CREATE INDEX IF NOT EXISTS idx_video_id ON recommended_videos(video_id);")
	if err != nil {
		log.Fatal(err)
	}

	// Initialize lastRecommendations from database
	loadRecentRecommendationsFromDB()
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

func loadRecentRecommendationsFromDB() {
	// Clear current in-memory list
	lastRecommendations = []string{}

	// Get recommendations from the last 7 days
	rows, err := db.Query("SELECT video_id FROM recommended_videos WHERE timestamp > datetime('now', '-7 day') ORDER BY timestamp DESC LIMIT 100")
	if err != nil {
		log.Printf("Error loading recommendations from DB: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var videoID string
		if err := rows.Scan(&videoID); err == nil {
			lastRecommendations = append(lastRecommendations, videoID)
		}
	}

	log.Printf("Loaded %d recent recommendations from database", len(lastRecommendations))
}

// Function to store a recommendation in the database
func storeRecommendationInDB(videoID string) {
	// First, attempt to insert the video ID
	_, err := db.Exec("INSERT OR IGNORE INTO recommended_videos (video_id) VALUES (?)", videoID)
	if err != nil {
		log.Printf("Error storing recommendation in DB: %v", err)
	}

	// Then clean up old recommendations (keep only last 200)
	_, err = db.Exec("DELETE FROM recommended_videos WHERE id NOT IN (SELECT id FROM recommended_videos ORDER BY timestamp DESC LIMIT 200)")
	if err != nil {
		log.Printf("Error cleaning up old recommendations: %v", err)
	}
}

// Modify the getRecommendedVideo function
func getRecommendedVideo(w http.ResponseWriter, r *http.Request) {
	// Get the most recently played video ID (if any)
	var lastVideo YouTubeURL
	err := db.QueryRow("SELECT id, title, url, user FROM youtube_urls ORDER BY id DESC LIMIT 1").Scan(&lastVideo.ID, &lastVideo.Title, &lastVideo.URL, &lastVideo.User)

	// Default search queries for variety when no specific relatedness is available
	searchQueries := []string{
		"official music",
	}

	// Avoid compilation keywords
	compilationKeywords := []string{
		"compilation", "playlist", "mix", "mashup", "megamix",
		"collection", "best of", "top 10", "top 20", "medley",
		"hits of", "greatest hits", "hour", "complete album",
		"songs", "tracks", "compilation", "non stop", "nonstop",
		"back to back", "b2b", "music collection", "jukeboxes", "jukebox",
		"all songs", "audio songs", "video songs", "chart", "songs",
	}

	// Randomly select a search query if we need to use one
	rand.Seed(time.Now().UnixNano())
	searchQuery := searchQueries[rand.Intn(len(searchQueries))]

	// Number of results to request - increased for more variety
	maxResults := "25" // Increased from 10 to 25

	client := resty.New()
	baseURL := "https://www.googleapis.com/youtube/v3/search"
	params := url.Values{}
	params.Set("part", "snippet")
	params.Set("type", "video")
	params.Set("videoCategoryId", "10") // 10 is for Music category
	params.Set("maxResults", maxResults)
	params.Set("key", apiKey)
	params.Set("relevanceLanguage", "en") // Prefer English content
	params.Set("videoDuration", "short")  // Prefer shorter videos (more likely single songs)

	// If we have a previous video, try to get related content
	if err == nil && lastVideo.URL != "" {
		videoID := extractVideoID(lastVideo.URL)
		if videoID != "" {
			// 70% chance to get a related video, 30% chance to get a random one
			useRelated := rand.Float32() < 0.7

			if useRelated {
				params.Set("relatedToVideoId", videoID)

				resp, err := client.R().
					SetQueryString(params.Encode()).
					Get(baseURL)

				if err == nil && resp.StatusCode() == 200 {
					var result map[string]interface{}
					if err := json.Unmarshal(resp.Body(), &result); err == nil {
						items, ok := result["items"].([]interface{})
						if ok && len(items) > 0 {
							// We have results, filter and pick a random one
							filteredItems := filterCompilations(items, compilationKeywords)
							if len(filteredItems) > 0 {
								recommendation := getRandomRecommendation(filteredItems)

								// Store the recommendation in database
								videoID := extractVideoID(recommendation.URL)
								if videoID != "" {
									storeRecommendationInDB(videoID)
								}

								w.Header().Set("Content-Type", "application/json")
								json.NewEncoder(w).Encode(recommendation)
								return
							}
						}
					}
				}
			}
			// If useRelated is false or if related search failed, we'll fall through to random search
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

	// Filter items to exclude compilations
	filteredItems := filterCompilations(items, compilationKeywords)
	if len(filteredItems) == 0 {
		// Instead of using all items, get a random subset to avoid repeating the same songs
		if len(items) > 5 {
			// Shuffle the items
			rand.Shuffle(len(items), func(i, j int) {
				items[i], items[j] = items[j], items[i]
			})
			// Take just a few
			filteredItems = items[:5]
		} else {
			filteredItems = items
		}
	}

	// Get a random recommendation
	recommendation := getRandomRecommendation(filteredItems)

	// Store the recommendation in database
	videoID := extractVideoID(recommendation.URL)
	if videoID != "" {
		storeRecommendationInDB(videoID)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(recommendation)
}

// Helper function for min value
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var excludedKeywords = []string{
	"indian", "hindi", "bollywood", "tamil", "telugu", "punjabi",
	"bhangra", "desi", "carnatic", "bharatanatyam",
}

// Helper function to filter out compilations/playlists and excluded content
func filterCompilations(items []interface{}, compilationKeywords []string) []interface{} {
	var filteredItems []interface{}

	// Get the excluded keywords from the global variable
	excludedKeywords := excludedKeywords // Using the globally defined excluded keywords

	for _, item := range items {
		snippet, ok := item.(map[string]interface{})["snippet"].(map[string]interface{})
		if !ok {
			continue
		}

		// Extract title, description and channel title
		title, titleOk := snippet["title"].(string)
		description, descOk := snippet["description"].(string)
		channelTitle, channelOk := snippet["channelTitle"].(string)

		if !titleOk {
			continue
		}

		// Check if this is likely a compilation or excluded content
		shouldFilter := false

		// Convert to lowercase for case-insensitive matching
		titleLower := strings.ToLower(title)

		// Check title length - extremely long titles often indicate compilations
		if len(titleLower) > 70 {
			// Long titles are more suspicious, check more carefully
			wordCount := len(strings.Fields(titleLower))
			if wordCount > 10 {
				// Lots of words often means a compilation listing multiple songs
				shouldFilter = true
			}
		}

		// Check for compilation keywords in title
		if !shouldFilter {
			for _, keyword := range compilationKeywords {
				if strings.Contains(titleLower, strings.ToLower(keyword)) {
					shouldFilter = true
					break
				}
			}
		}

		// Check for excluded keywords in title
		if !shouldFilter {
			for _, keyword := range excludedKeywords {
				if strings.Contains(titleLower, strings.ToLower(keyword)) {
					shouldFilter = true
					break
				}
			}
		}

		// Check channel title for excluded keywords
		if !shouldFilter && channelOk {
			channelLower := strings.ToLower(channelTitle)
			for _, keyword := range excludedKeywords {
				if strings.Contains(channelLower, strings.ToLower(keyword)) {
					shouldFilter = true
					break
				}
			}
		}

		// Check for durations in title (like "10 minutes", "1 hour", etc.)
		if !shouldFilter && (strings.Contains(titleLower, "minute") ||
			strings.Contains(titleLower, "hour") ||
			regexp.MustCompile(`\d+\s*min`).MatchString(titleLower)) {
			shouldFilter = true
		}

		// Also check description if available
		if !shouldFilter && descOk {
			descLower := strings.ToLower(description)

			// Check for compilation keywords in description
			for _, keyword := range compilationKeywords {
				if strings.Contains(descLower, strings.ToLower(keyword)) {
					shouldFilter = true
					break
				}
			}

			// Check for excluded keywords in description
			if !shouldFilter {
				for _, keyword := range excludedKeywords {
					if strings.Contains(descLower, strings.ToLower(keyword)) {
						shouldFilter = true
						break
					}
				}
			}
		}

		// If it passed all the filters, add it to filtered items
		if !shouldFilter {
			filteredItems = append(filteredItems, item)
		}
	}

	return filteredItems
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
