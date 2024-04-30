package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	_ "github.com/tursodatabase/libsql-client-go/libsql"
)

type Post struct {
	ID   int    `json:"id"`
	Body string `json:"body"`
}

var (
	db       *sql.DB
	upgrader = websocket.Upgrader{}
	conn     *websocket.Conn
)

func main() {

	err := godotenv.Load(".env")
	if err != nil {
		log.Fatalf("Error loading .env file: %s", err)
	}

	path := os.Getenv("TURSO_DATABASE_URL")
	token := os.Getenv("TURSO_AUTH_TOKEN")

	url := fmt.Sprintf("%s?authToken=%s", path, token)

	// init database connection
	db, err = sql.Open("libsql", url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open db %s: %s", url, err)
		os.Exit(1)
	}

	// routes
	http.HandleFunc("/posts", postsHandler)
	http.HandleFunc("/posts/", postHandler)
	http.HandleFunc("/*", handleHtml)
	http.HandleFunc("/socket", socketHandler)

	fmt.Println("Server is running at http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
	defer db.Close()
}
func socketHandler(w http.ResponseWriter, r *http.Request) {
	var err error
	conn, err = upgrader.Upgrade(w, r, nil)

	if err != nil {
		log.Print("upgrade failed: ", err)
		return
	}

	defer conn.Close()

	for {
		mt, message, err := conn.ReadMessage()
		if err != nil {
			log.Println("read failed: ", err)
			break
		}

		fmt.Println(mt, string(message))

		conn.WriteMessage(mt, []byte("My message from the server, clown"))
	}
}

func postsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		handleGetPosts(w, r)
	case "POST":
		handlePostPosts(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func postHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.URL.Path[len("/posts/"):])
	if err != nil {
		http.Error(w, "Invalid Post ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case "GET":
		handleGetPost(w, r, id)
	case "DELETE":
		handleDeletePost(w, r, id)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleHtml(w http.ResponseWriter, r *http.Request) {

	postsResult := getPosts(w)

	var postsString string

	for _, p := range postsResult {
		postsString += fmt.Sprintf(`<li>{"id":%d,"body":%s}</li>`, p.ID, p.Body)
	}

	str := fmt.Sprintf(`
	<html>
	<head>
	<style>
	* {
		font-family: monospace;
	}
	</style>
	</head>
	<body>
	<button id="pingSocket" onclick="pingSocket">Socket Ping</button>

	<button id="createPost">Create Post</button>
	<ul id="posts">
		%s
	</ul>
	<script>
		let items = document.getElementById("posts");

		var socket = new WebSocket("ws://localhost:8080/socket");

		socket.onopen = function () {
		  console.log("Status: Connected");
		};
	
		socket.onmessage = function (e) {
		  console.log("Server: " + e.data);

		  try {
			const response = JSON.parse(e.data)

			handleWsResponses(response)

		  } catch (err) {
			console.error("not json", err)
		  }
		};

		function handleWsResponses(message){
			if(message.messageType === "new"){
				items.innerHTML += '<li>' + JSON.stringify(message.post) + '</li>'
			}
		}
		function pingSocket() {
			console.log("is socket ready?", socket)
			if(!socket){
				console.log("not connected to socket server")
				return
			}
			socket.send("ping")	
		}

		function createPostHandler(){
			fetch("/posts", {
				method: "POST",
				body: JSON.stringify({body: Math.random().toString()})
			}).then((res) => res.json()).then(data => {
				console.log(data)

				items.innerHTML += '<li>' + JSON.stringify(data) + '</li>'
			}).catch(err => {
				console.error(err)
			})
		}
		
		document.getElementById("createPost").addEventListener("click", createPostHandler);
		document.getElementById("pingSocket").addEventListener("click", pingSocket);

	</script>
	</body>
	</html>
	`, postsString)

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(str))
}

func handleGetPosts(w http.ResponseWriter, r *http.Request) {

	ps := getPosts(w)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ps)
}

func getPosts(w http.ResponseWriter) []Post {

	rows, err := db.Query("SELECT * FROM posts")
	if err != nil {
		http.Error(w, "Failed to execute query", http.StatusInternalServerError)
	}

	defer rows.Close()

	var ps []Post

	for rows.Next() {
		var post Post

		if err := rows.Scan(&post.ID, &post.Body); err != nil {
			fmt.Println("Error scanning row:", err)
			http.Error(w, "Failed to execute query", http.StatusInternalServerError)
		}

		ps = append(ps, post)
	}

	if err := rows.Err(); err != nil {
		fmt.Println("Error during rows iteration:", err)
	}

	return ps
}

type WSPush struct {
	MessageType string `json:"messageType"`
	Post        Post   `json:"post"`
}

func handlePostPosts(w http.ResponseWriter, r *http.Request) {
	var p Post
	var res WSPush

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusBadRequest)
	}

	if err := json.Unmarshal(body, &p); err != nil {
		http.Error(w, "Error parsing request body", http.StatusBadRequest)
	}

	result, err := db.Exec(fmt.Sprintf("INSERT INTO posts (body) VALUES (%s)", p.Body))
	if err != nil {
		http.Error(w, "Error creating post", http.StatusBadRequest)
	}

	lastID, err := result.LastInsertId()
	if err != nil {
		http.Error(w, "Error creating post", http.StatusBadRequest)
	}

	p.ID = int(lastID)

	res.MessageType = "new"
	res.Post = p

	conn.WriteJSON(res)

	w.Header().Set("Content-Type", "application/json")
	// w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(p)

}

func handleGetPost(w http.ResponseWriter, r *http.Request, id int) {
	p := getPostById(w, id)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p)
}

func getPostById(w http.ResponseWriter, id int) Post {

	var post Post

	err := db.QueryRow(fmt.Sprintf("SELECT * FROM posts WHERE id = %d LIMIT 1", id)).Scan(&post.ID, &post.Body)
	if err != nil {
		log.Println(err)
		http.Error(w, "Failed to execute query", http.StatusInternalServerError)
	}

	return post
}

func handleDeletePost(w http.ResponseWriter, r *http.Request, id int) {
	isDeleted := deletePostById(w, id)
	if isDeleted == 0 {
		http.Error(w, "Post not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func deletePostById(w http.ResponseWriter, id int) int {

	result, err := db.Exec(fmt.Sprintf("DELETE FROM posts WHERE id = %d", id))
	if err != nil {
		log.Println(err)
		http.Error(w, "Failed to execute query", http.StatusInternalServerError)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		log.Println(err)
		http.Error(w, "Failed to execute query", http.StatusInternalServerError)
	}
	return int(rows)
}
