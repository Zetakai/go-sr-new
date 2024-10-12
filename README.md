# 🎵 Song Request Manager

A simple **Go-based application** that allows users to request YouTube songs by title. The app retrieves the **closest YouTube video match** using the YouTube Data API and manages song queues. This project demonstrates how to integrate a Go backend, SQLite database, and frontend with API calls to the YouTube API.

---

## 📋 Features
- Add songs by title and retrieve the closest matching YouTube video.
- Store the **video title and URL** in a local SQLite database.
- Manage the song queue (add, delete, and play songs in order).
- **YouTube Player integration** for autoplaying the requested videos.

---

## 📂 Project Setup

### Clone the repository:

```bash
git clone https://github.com/Zetakai/go-sr-new.git
cd go-sr-new
```

---

Install dependencies:
In the project directory, run:

bash
```bash
go mod tidy
```
Set up your .env file:
Create a new .env file in the root of the project:

```bash
touch .env
```
Open the .env file and add the following line:

```makefile
YOUTUBE_API_KEY=xxx
```
Replace xxx with your actual YouTube API key.
You can get a key by following the YouTube Data API documentation.

Make sure the .env file is ignored by Git:
Add the following line to your .gitignore file (if it isn’t already there):

```bash
.env
```
This ensures your API key remains private and isn’t accidentally uploaded to version control.

---

## 🛠️ Running the Application
Start the server:

```bash
go run main.go
```
Access the application:
Open your browser and visit:
http://localhost:420

---

## 📄 API Endpoints
Endpoint	Method	Description
- /	GET	Loads the requester frontend.
- /host	GET	Loads the host frontend.
- /url	POST	Adds a new song to the queue.
- /url	DELETE	Removes a song from the queue.
- /url/oldest	GET	Gets and deletes the oldest song.
- /urls	GET	Lists all songs in the queue.

---
  
## 🎨 Frontend
requester.html:
Allows users to add songs to the queue by entering a song title. It automatically fetches the closest matching YouTube video.

host.html:
Displays the song queue and lets the host manage songs and play them through the YouTube player.

---

## ⚠️ Troubleshooting
Error loading API key:
Make sure the .env file is correctly set up and located in the root directory. If you see:


YOUTUBE_API_KEY not found in environment
Ensure the key is correctly added to your .env file.

YouTube API errors:
Ensure your API key is valid and has access to the YouTube Data API v3.
Check your API quota on the Google Cloud Console.

---

## 📜 License
This project is licensed under the MIT License.

---

## 📧 Contact
For any questions or feedback, feel free to reach out at thecocoanutmedia@gmail.com.
