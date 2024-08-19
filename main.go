package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	//"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/time/rate"
)

type LogEntry struct {
    Request *Request `json:"request"`
    Capture *Capture `json:"capture"`
}

type TemplateData struct {
    Title string
    Logs  []LogEntry
	Geocache string
}

type Capture struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CaptureAt string `json:"capture_at"`
}

type Request struct {
    Method     string `json:"method"`
    URI        string `json:"uri"`
    RealIP     string `json:"real_ip"`
    ForwardedIP string `json:"forwarded_ip"`
}

type IPRateLimiter struct {
    ips map[string]*rate.Limiter
    mu  *sync.RWMutex
    r   rate.Limit
    b   int
    firstHit map[string]bool
}

func NewIPRateLimiter(r rate.Limit, b int) *IPRateLimiter {
    return &IPRateLimiter{
        ips: make(map[string]*rate.Limiter),
        mu:  &sync.RWMutex{},
        r:   r,
        b:   b,
        firstHit: make(map[string]bool),
    }
}

func (i *IPRateLimiter) AddIP(ip string) *rate.Limiter {
    i.mu.Lock()
    defer i.mu.Unlock()

    limiter := rate.NewLimiter(i.r, i.b)

    i.ips[ip] = limiter

    return limiter
}

func (i *IPRateLimiter) GetLimiter(ip string) (*rate.Limiter, bool) {
    i.mu.Lock()
    defer i.mu.Unlock()

    limiter, exists := i.ips[ip]
    if !exists {
        limiter = rate.NewLimiter(i.r, i.b)
        i.ips[ip] = limiter
    }

    firstHit := i.firstHit[ip]
    if !firstHit {
        i.firstHit[ip] = true
        return limiter, false
    }

    return limiter, true
}

func RateLimitMiddleware(limiter *IPRateLimiter) gin.HandlerFunc {
    return func(c *gin.Context) {
        ip := c.ClientIP()
        limiter, shouldLimit := limiter.GetLimiter(ip)
        if shouldLimit && !limiter.Allow() {
            nextAllowedTime := time.Now().Add(24 * time.Hour)
            c.HTML(http.StatusTooManyRequests, "rate_limit_exceeded.html", gin.H{
                "Title": "Rate Limit Exceeded",
                "NextAllowedTime": nextAllowedTime.Format("2006-01-02 15:04:05"),
            })
            c.Abort()
            return
        }
        c.Next()
    }
}

func handleRegister(c *gin.Context) {
	geocacheName := c.DefaultQuery("geocache", "Guest")
    logs, err := readLogs(geocacheName)
    if err != nil {
        c.String(http.StatusInternalServerError, "Error reading log file")
        return
    }

    var logEntries []LogEntry
    scanner := bufio.NewScanner(strings.NewReader(logs))
    for scanner.Scan() {
        var entry LogEntry
        if err := json.Unmarshal(scanner.Bytes(), &entry); err == nil {
            logEntries = append(logEntries, entry)
        }
    }

    data := TemplateData{
        Title: "Register",
        Logs:  logEntries,
		Geocache: geocacheName,
    }

    c.HTML(http.StatusOK, "layout.html", data)
}

func handleCapture(c *gin.Context) {
	geocacheName := c.DefaultQuery("geocache", "Guest")
	name := c.PostForm("name")
	capture := Capture{
		ID:        uuid.New().String(), // Genera un UUID v7
		Name:      name,
		CaptureAt: time.Now().Format(time.RFC3339),
	}

	request := Request{
        Method:     c.Request.Method,
        URI:        c.Request.URL.Path,
        RealIP:     c.ClientIP(),
        ForwardedIP: c.GetHeader("X-Forwarded-For"),
    }

	registerRequest(geocacheName, &request, &capture)
	c.String(http.StatusOK, "Successful capture")
}

func handleLogs(c *gin.Context) {
	geocacheName := c.DefaultQuery("geocache", "Guest")
	logs, err := readLogs(geocacheName)
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
			</p>
		`, logEntry.Capture.ID, logEntry.Capture.Name, logEntry.Capture.CaptureAt)

		html += entryHTML
	}

	html += "</div>"

	// Devolver el HTML completo
	c.Header("Content-Type", "text/html")
	c.String(http.StatusOK, html)
}


func registerRequest(filename string, req *Request, capture *Capture) {
	// check if filename exist or create it
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		file, err := os.Create(filename)
		if err != nil {
			fmt.Println(err)
			return
		}
		defer file.Close()
	}

	f, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
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

func readLogs(filename  string) (string, error) {
	// check if filename exist or create it
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		file, err := os.Create(filename)
		if err != nil {
			return "", err
		}
		defer file.Close()
	}
	f, err := os.OpenFile(filename, os.O_RDONLY, 0666)
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


func main() {
	router := gin.Default()
	router.LoadHTMLGlob(filepath.Join("assets", "templates", "*"))
	router.StaticFile("/favicon.ico", filepath.Join("assets","resources","favicon.ico"))
	router.StaticFile("/logo_380.webp", filepath.Join("assets","resources","logo_380.webp"))

	router.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", gin.H{
			"title": "Geocaching",
		})
	})

	v1 := router.Group("api/v1")
    
	// Crear un rate limiter que permite 1 solicitud por día
    limiter := NewIPRateLimiter(rate.Limit(1.0/86400), 3) // 86400 segundos en un día

    // Aplicar el middleware solo al endpoint /register
    v1.GET("/register", RateLimitMiddleware(limiter), handleRegister)
	v1.POST("/capture", RateLimitMiddleware(limiter), handleCapture)
	v1.GET("/logs", handleLogs)

	router.Run(":3080")
}