package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

)

type Capture struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CaptureAt string `json:"capture_at"`
}

type Request struct {
	Method string `json:"method"`
	URI    string `json:"uri"`
}

type IPRateLimiter struct {
    ips map[string]*rate.Limiter
    mu  *sync.RWMutex
    r   rate.Limit
    b   int
}

func NewIPRateLimiter(r rate.Limit, b int) *IPRateLimiter {
    i := &IPRateLimiter{
        ips: make(map[string]*rate.Limiter),
        mu:  &sync.RWMutex{},
        r:   r,
        b:   b,
    }

    return i
}

func (i *IPRateLimiter) AddIP(ip string) *rate.Limiter {
    i.mu.Lock()
    defer i.mu.Unlock()

    limiter := rate.NewLimiter(i.r, i.b)

    i.ips[ip] = limiter

    return limiter
}

func (i *IPRateLimiter) GetLimiter(ip string) *rate.Limiter {
    i.mu.Lock()
    limiter, exists := i.ips[ip]

    if !exists {
        i.mu.Unlock()
        return i.AddIP(ip)
    }

    i.mu.Unlock()

    return limiter
}

func main() {
	router := gin.Default()
	v1 := router.Group("api/v1")
    
	// Crear un rate limiter que permite 1 solicitud por día
    limiter := NewIPRateLimiter(rate.Limit(1.0/86400), 1) // 86400 segundos en un día

    // Aplicar el middleware solo al endpoint /register
    v1.GET("/register", RateLimitMiddleware(limiter), handleRegister)
	v1.POST("/capture", handleCapture)
	v1.GET("/logs", handleLogs)

	router.Run(":3080")
}

func RateLimitMiddleware(limiter *IPRateLimiter) gin.HandlerFunc {
    return func(c *gin.Context) {
        ip := c.ClientIP()
        limiter := limiter.GetLimiter(ip)
        if !limiter.Allow() {
            c.String(http.StatusTooManyRequests, "Rate limit exceeded")
            c.Abort()
            return
        }
        c.Next()
    }
}

func handleRegister(c *gin.Context) {
	logs, err := readLogs()
	if err != nil {
		c.String(http.StatusInternalServerError, "Error reading log file")
		return
	}

	// Estilo CSS para los párrafos y el formulario
	style := `
	<style>
		body {
			font-family: Arial, sans-serif;
			max-width: 800px;
			margin: 0 auto;
			padding: 20px;
		}
		.form-container {
			background-color: #f9f9f9;
			border: 1px solid #ddd;
			border-radius: 5px;
			padding: 20px;
			margin-bottom: 20px;
		}
		input[type="text"] {
			width: 100%;
			padding: 10px;
			margin-bottom: 10px;
			border: 1px solid #ddd;
			border-radius: 4px;
		}
		button {
			background-color: #4CAF50;
			color: white;
			padding: 10px 15px;
			border: none;
			border-radius: 4px;
			cursor: pointer;
		}
		button:hover {
			background-color: #45a049;
		}
		.log-entry {
			background-color: #f0f0f0;
			border: 1px solid #ddd;
			border-radius: 5px;
			padding: 10px;
			margin-bottom: 10px;
		}
	</style>
	`

	// Formulario HTML
	form := `
	<div class="form-container">
		<h2>Register</h2>
		<form hx-post="/api/v1/capture" hx-swap="outerHTML">
			<input type="text" name="name" placeholder="Enter your name">
			<button type="submit">Submit</button>
		</form>
	</div>
	`

	// Iniciar el contenido HTML
	html := style + "<script src='https://unpkg.com/htmx.org@2.0.1'></script>" + form + "<h2>Logs</h2><div>"

	// Procesar cada línea del log
	scanner := bufio.NewScanner(strings.NewReader(logs))
	for scanner.Scan() {
		line := scanner.Text()
		var logEntry struct {
			Request *Request `json:"request"`
			Capture *Capture `json:"capture"`
		}
		err := json.Unmarshal([]byte(line), &logEntry)
		if err != nil {
			continue
		}

		entryHTML := fmt.Sprintf(`
			<div class="log-entry">
				<strong>ID:</strong> %s<br>
				<strong>Name:</strong> %s<br>
				<strong>Capture At:</strong> %s<br>
				<strong>Method:</strong> %s<br>
				<strong>URI:</strong> %s
			</div>
		`, logEntry.Capture.ID, logEntry.Capture.Name, logEntry.Capture.CaptureAt, 
		   logEntry.Request.Method, logEntry.Request.URI)

		html += entryHTML
	}

	html += "</div>"

	c.Header("Content-Type", "text/html")
	c.String(http.StatusOK, html)
}

func handleCapture(c *gin.Context) {
	name := c.PostForm("name")
	capture := Capture{
		ID:        uuid.New().String(), // Genera un UUID v7
		Name:      name,
		CaptureAt: time.Now().Format(time.RFC3339),
	}

	registerRequest(&Request{Method: c.Request.Method, URI: c.Request.URL.Path}, &capture)
	c.String(http.StatusOK, "Captura realizada con éxito")
}

func handleLogs(c *gin.Context) {
	logs, err := readLogs()
	if err != nil {
		c.String(http.StatusInternalServerError, "Error reading log file")
		return
	}

	// Estilo CSS para los párrafos
	style := `
	<style>
		.log-entry {
			background-color: #f0f0f0;
			border: 1px solid #ddd;
			border-radius: 5px;
			padding: 10px;
			margin-bottom: 10px;
			font-family: Arial, sans-serif;
		}
	</style>
	`

	// Iniciar el contenido HTML
	html := style + "<div>"

	// Procesar cada línea del log
	scanner := bufio.NewScanner(strings.NewReader(string(logs)))
	for scanner.Scan() {
		line := scanner.Text()
		// Decodificar el JSON de cada línea
		var logEntry struct {
			Request *Request `json:"request"`
			Capture *Capture `json:"capture"`
		}
		err := json.Unmarshal([]byte(line), &logEntry)
		if err != nil {
			continue // Saltar líneas que no se puedan decodificar
		}

		// Crear un párrafo HTML para cada entrada del log
		entryHTML := fmt.Sprintf(`
			<p class="log-entry">
				<strong>ID:</strong> %s<br>
				<strong>Name:</strong> %s<br>
				<strong>Capture At:</strong> %s<br>
				<strong>Method:</strong> %s<br>
				<strong>URI:</strong> %s
			</p>
		`, logEntry.Capture.ID, logEntry.Capture.Name, logEntry.Capture.CaptureAt, 
		   logEntry.Request.Method, logEntry.Request.URI)

		html += entryHTML
	}

	html += "</div>"

	// Devolver el HTML completo
	c.Header("Content-Type", "text/html")
	c.String(http.StatusOK, html)
}


func registerRequest(req *Request, capture *Capture) {
	f, err := os.OpenFile("captures.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer f.Close()

	jsonData, err := json.Marshal(struct {
		Request *Request `json:"request"`
		Capture *Capture `json:"capture"`
	}{req, capture})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Fprintln(f, string(jsonData))
}

func readLogs() (string, error) {
	f, err := os.OpenFile("captures.log", os.O_RDONLY, 0666)
	if err != nil {
		return "", err
	}
	defer f.Close()

	bytes, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}