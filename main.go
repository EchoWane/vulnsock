package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	_ "github.com/mattn/go-sqlite3"
)

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	clients   = make(map[*websocket.Conn]string)
	clientsMu sync.Mutex
	broadcast = make(chan Message, 100)
	db        *sql.DB
)

type Message struct {
	Type     string `json:"type"`
	Username string `json:"username"`
	Content  string `json:"content"`
	Time     string `json:"time"`
	Channel  string `json:"channel"`
}

type UserPreferences struct {
	Theme         string `json:"theme"`
	Notifications bool   `json:"notifications"`
	Language      string `json:"language"`
}

func main() {
	var err error
	db, err = sql.Open("sqlite3", "./vulnsock.db")
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer db.Close()

	initDB()

	http.HandleFunc("/", serveHome)
	http.HandleFunc("/ws", handleWebSocket)
	http.HandleFunc("/api/users", handleUsers)
	http.HandleFunc("/api/broadcast", handleBroadcast)
	http.HandleFunc("/api/search", handleSearch)
	http.HandleFunc("/api/preferences", handlePreferences)

	go handleMessages()

	port := "8080"
	log.Printf("Server starting on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func initDB() {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL,
			username TEXT NOT NULL,
			content TEXT NOT NULL,
			time TEXT NOT NULL,
			channel TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		log.Fatal("Failed to create messages table:", err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL,
			remote_addr TEXT,
			local_addr TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_seen DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		log.Fatal("Failed to create users table:", err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS preferences (
			username TEXT PRIMARY KEY,
			theme TEXT DEFAULT 'light',
			notifications BOOLEAN DEFAULT 1,
			language TEXT DEFAULT 'en'
		)
	`)
	if err != nil {
		log.Fatal("Failed to create preferences table:", err)
	}

	log.Println("Database initialized successfully")
}

func serveHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	http.ServeFile(w, r, "index.html")
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	defer conn.Close()

	var initMsg Message
	err = conn.ReadJSON(&initMsg)
	if err != nil {
		log.Println("Failed to read initial message:", err)
		return
	}

	username := initMsg.Username
	if username == "" {
		username = "Anonymous"
	}

	clientsMu.Lock()
	clients[conn] = username
	clientsMu.Unlock()

	rows, err := db.Query("SELECT type, username, content, time, channel FROM messages ORDER BY id ASC LIMIT 100")
	if err != nil {
		log.Println("Failed to load message history:", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var msg Message
			err := rows.Scan(&msg.Type, &msg.Username, &msg.Content, &msg.Time, &msg.Channel)
			if err != nil {
				log.Println("Failed to scan message:", err)
				continue
			}
			conn.WriteJSON(msg)
		}
	}

	hostname := os.Getenv("HOSTNAME")
	if hostname == "" {
		hostname = "teamchat-prod"
	}
	serverInfo := Message{
		Type:    "system",
		Content: fmt.Sprintf("Connected to %s (v1.2.3) • %d users online", hostname, len(clients)),
		Time:    time.Now().Format("15:04:05"),
	}
	conn.WriteJSON(serverInfo)

	if initMsg.Content != "" {
		initMsg.Time = time.Now().Format("15:04:05")
		broadcast <- initMsg
	}

	for {
		var msg Message
		err := conn.ReadJSON(&msg)
		if err != nil {
			clientsMu.Lock()
			delete(clients, conn)
			clientsMu.Unlock()
			break
		}

		msg.Time = time.Now().Format("15:04:05")
		broadcast <- msg
	}
}

func handleUsers(w http.ResponseWriter, r *http.Request) {
	clientsMu.Lock()
	defer clientsMu.Unlock()

	users := make([]map[string]string, 0)
	for conn, username := range clients {
		users = append(users, map[string]string{
			"username":    username,
			"remote_addr": conn.RemoteAddr().String(),
			"local_addr":  conn.LocalAddr().String(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"count": len(users),
		"users": users,
	})
}

func handleBroadcast(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg Message
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	msg.Time = time.Now().Format("15:04:05")
	broadcast <- msg

	w.WriteHeader(http.StatusOK)
}

func handleMessages() {
	for {
		msg := <-broadcast

		if msg.Type != "system" {
			channel := msg.Channel
			if channel == "" {
				channel = "general"
			}

			_, err := db.Exec(
				"INSERT INTO messages (type, username, content, time, channel) VALUES (?, ?, ?, ?, ?)",
				msg.Type, msg.Username, msg.Content, msg.Time, channel,
			)
			if err != nil {
				log.Println("Failed to save message to database:", err)
			}

			db.Exec("DELETE FROM messages WHERE id NOT IN (SELECT id FROM messages WHERE channel = ? ORDER BY id DESC LIMIT 100)", channel)
		}

		clientsMu.Lock()
		for client := range clients {
			err := client.WriteJSON(msg)
			if err != nil {
				client.Close()
				delete(clients, client)
			}
		}
		clientsMu.Unlock()
	}
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	query := r.URL.Query().Get("q")
	channel := r.URL.Query().Get("channel")

	if query == "" {
		http.Error(w, "Query parameter 'q' is required", http.StatusBadRequest)
		return
	}

	if channel == "" {
		channel = "general"
	}

	sqlQuery := fmt.Sprintf("SELECT id, username, content, time FROM messages WHERE channel = '%s' AND content LIKE '%%%s%%' ORDER BY id DESC LIMIT 10", channel, query)

	rows, err := db.Query(sqlQuery)
	if err != nil {
		http.Error(w, "Search error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	results := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id int
		var username, content, timeStr string
		err := rows.Scan(&id, &username, &content, &timeStr)
		if err != nil {
			log.Println("Failed to scan result:", err)
			continue
		}

		results = append(results, map[string]interface{}{
			"id":       id,
			"username": username,
			"content":  content,
			"time":     timeStr,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"results": results,
		"count":   len(results),
	})
}

func handlePreferences(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("username")
	if username == "" {
		http.Error(w, "Username required", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case "GET":
		var prefs UserPreferences
		row := db.QueryRow("SELECT theme, notifications, language FROM preferences WHERE username = ?", username)
		err := row.Scan(&prefs.Theme, &prefs.Notifications, &prefs.Language)
		if err == sql.ErrNoRows {
			prefs = UserPreferences{Theme: "light", Notifications: true, Language: "en"}
		} else if err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(prefs)

	case "POST":
		var prefs UserPreferences
		if err := json.NewDecoder(r.Body).Decode(&prefs); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		query := fmt.Sprintf(
			"INSERT OR REPLACE INTO preferences (username, theme, notifications, language) VALUES ('%s', '%s', %t, '%s')",
			username, prefs.Theme, prefs.Notifications, prefs.Language,
		)

		_, err := db.Exec(query)
		if err != nil {
			log.Printf("Failed to save preferences: %v", err)
			http.Error(w, "Failed to save preferences", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
