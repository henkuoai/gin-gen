package main

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"archive/zip"
	"io"

	"github.com/gin-gonic/gin"
)

// 项目配置结构
type ProjectConfig struct {
	ProjectName string
	ModuleName  string
	Port        string
}

// 模型字段结构
type ModelField struct {
	Name     string
	Type     string
	JsonTag  string
	GormTag  string
	Required bool
}

// 模型结构
type Model struct {
	Name       string
	Fields     []ModelField
	SnakeName  string
	LowerName  string
	PluralName string
}

// 模板数据
type TemplateData struct {
	Project   ProjectConfig
	Models    []Model
	Timestamp string
}

const dockerfileTemplate = `FROM golang:1.20-alpine AS builder

WORKDIR /app
COPY . .
RUN go mod download
RUN go build -o main cmd/main.go

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/main .
COPY --from=builder /app/.env .

EXPOSE {{.Project.Port}}
CMD ["./main"]
`

const gitignoreTemplate = `# Binaries
*.exe
*.exe~
*.dll
*.so
*.dylib
`

// 创建ZIP文件函数
func createZip(sourceDir, targetZip string) error {
	zipFile, err := os.Create(targetZip)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	return filepath.Walk(sourceDir, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// 跳过目录本身
		if info.IsDir() {
			return nil
		}

		// 创建ZIP文件中的文件头
		relPath, err := filepath.Rel(sourceDir, filePath)
		if err != nil {
			return err
		}

		zipHeader, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		zipHeader.Name = filepath.ToSlash(relPath)
		zipHeader.Method = zip.Deflate

		zipEntry, err := zipWriter.CreateHeader(zipHeader)
		if err != nil {
			return err
		}

		// 打开源文件
		srcFile, err := os.Open(filePath)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		// 复制文件内容到ZIP条目
		_, err = io.Copy(zipEntry, srcFile)
		return err
	})
}

func main() {
	router := gin.Default()
	router.Static("/static", "./static")
	router.LoadHTMLGlob("templates/*")

	// 首页
	router.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", gin.H{
			"title": "Gin CRUD 代码生成器",
		})
	})

	// 生成项目
	router.POST("/generate", func(c *gin.Context) {
		// 解析表单数据
		projectName := c.PostForm("project_name")
		moduleName := c.PostForm("module_name")
		port := c.PostForm("port")
		models := parseModels(c.PostForm("models"))

		// 创建模板数据
		data := TemplateData{
			Project: ProjectConfig{
				ProjectName: projectName,
				ModuleName:  moduleName,
				Port:        port,
			},
			Models: models,
		}

		// 创建临时目录
		tempDir, err := os.MkdirTemp("", "gin-crud-*")
		if err != nil {
			log.Fatal(err)
		}
		defer os.RemoveAll(tempDir)

		// 生成项目结构
		generateProjectStructure(tempDir, data)

		// 创建ZIP文件
		zipPath := filepath.Join(os.TempDir(), projectName+".zip")
		if err := createZip(tempDir, zipPath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "无法创建ZIP文件: " + err.Error()})
			return
		}

		// 提供下载
		c.Header("Content-Description", "File Transfer")
		c.Header("Content-Disposition", "attachment; filename="+projectName+".zip")
		c.Header("Content-Type", "application/zip")
		c.File(zipPath)
	})

	// 启动服务器
	fmt.Println("Gin CRUD 代码生成器运行在 http://localhost:8080")
	router.Run(":8080")
}

// 解析模型定义
func parseModels(input string) []Model {
	var models []Model
	blocks := strings.Split(input, "\n\n")

	for _, block := range blocks {
		lines := strings.Split(block, "\n")
		if len(lines) < 2 {
			continue
		}

		modelName := strings.TrimSpace(lines[0])
		var fields []ModelField

		for _, line := range lines[1:] {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}

			fieldName := parts[0]
			fieldType := parts[1]
			jsonTag := strings.ToLower(fieldName)
			gormTag := ""

			// 处理字段标签
			if len(parts) > 2 {
				for _, tag := range parts[2:] {
					if strings.HasPrefix(tag, "gorm:") {
						gormTag = strings.Trim(tag, "gorm:\"")
					}
				}
			}

			// 默认gorm标签
			if gormTag == "" {
				gormTag = "column:" + toSnakeCase(fieldName)
			}

			fields = append(fields, ModelField{
				Name:     fieldName,
				Type:     fieldType,
				JsonTag:  jsonTag,
				GormTag:  gormTag,
				Required: strings.Contains(line, "required"),
			})
		}

		models = append(models, Model{
			Name:       modelName,
			Fields:     fields,
			SnakeName:  toSnakeCase(modelName),
			LowerName:  strings.ToLower(modelName[:1]) + modelName[1:],
			PluralName: pluralize(modelName),
		})
	}

	return models
}

// 生成项目结构
func generateProjectStructure(baseDir string, data TemplateData) {
	// 创建目录结构
	dirs := []string{
		"cmd",
		"pkg/api",
		"pkg/config",
		"pkg/database",
		"pkg/models",
		"pkg/handlers",
		"pkg/middlewares",
		"api",
		"migrations",
		"docs",
	}

	for _, dir := range dirs {
		os.MkdirAll(filepath.Join(baseDir, dir), 0755)
	}

	// 定义要生成的文件模板
	files := map[string]string{
		"cmd/main.go":               mainTemplate,
		"pkg/config/config.go":      configTemplate,
		"pkg/database/database.go":  databaseTemplate,
		"pkg/api/server.go":         serverTemplate,
		"pkg/middlewares/logger.go": loggerMiddlewareTemplate,
		".env":                      envTemplate,
		"go.mod":                    goModTemplate,
		"README.md":                 readmeTemplate,
		"Dockerfile":                dockerfileTemplate,
		".gitignore":                gitignoreTemplate,
	}

	// 为每个模型生成文件
	for _, model := range data.Models {
		modelFiles := map[string]string{
			"pkg/models/" + model.SnakeName + ".go":   modelTemplate,
			"pkg/handlers/" + model.SnakeName + ".go": handlerTemplate,
			"api/" + model.SnakeName + ".yaml":        apiSpecTemplate,
		}

		for path, tmpl := range modelFiles {
			generateFile(baseDir, path, tmpl, struct {
				Project ProjectConfig
				Model   Model
			}{data.Project, model})
		}
	}

	// 生成其他文件
	for path, tmpl := range files {
		generateFile(baseDir, path, tmpl, data)
	}
}

// 生成单个文件
func generateFile(baseDir, filePath, tmplContent string, data interface{}) {
	path := filepath.Join(baseDir, filePath)
	os.MkdirAll(filepath.Dir(path), 0755)

	tmpl, err := template.New(filePath).Parse(tmplContent)
	if err != nil {
		log.Fatalf("无法解析模板 %s: %v", filePath, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		log.Fatalf("无法执行模板 %s: %v", filePath, err)
	}

	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		log.Fatalf("无法写入文件 %s: %v", path, err)
	}
}

// 辅助函数：转换为蛇形命名
func toSnakeCase(s string) string {
	var result strings.Builder
	for i, c := range s {
		if i > 0 && 'A' <= c && c <= 'Z' {
			result.WriteByte('_')
		}
		result.WriteRune(c)
	}
	return strings.ToLower(result.String())
}

// 辅助函数：复数化
func pluralize(s string) string {
	if strings.HasSuffix(s, "y") {
		return strings.TrimSuffix(s, "y") + "ies"
	}
	return s + "s"
}

// 模板定义
const mainTemplate = `package main

import (
	"log"
	"{{.Project.ModuleName}}/pkg/api"
	"{{.Project.ModuleName}}/pkg/config"
	"{{.Project.ModuleName}}/pkg/database"
)

func main() {
	// 加载配置
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}

	// 初始化数据库
	db, err := database.InitDB(cfg)
	if err != nil {
		log.Fatalf("Error initializing database: %v", err)
	}

	// 创建API服务器
	server := api.NewServer(cfg, db)

	// 启动服务器
	if err := server.Run(); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}
`

const configTemplate = `package config

import (
	"github.com/spf13/viper"
)

type Config struct {
	AppPort  string ` + "`mapstructure:\"APP_PORT\"`" + `
	DBHost   string ` + "`mapstructure:\"DB_HOST\"`" + `
	DBPort   string ` + "`mapstructure:\"DB_PORT\"`" + `
	DBUser   string ` + "`mapstructure:\"DB_USER\"`" + `
	DBPass   string ` + "`mapstructure:\"DB_PASSWORD\"`" + `
	DBName   string ` + "`mapstructure:\"DB_NAME\"`" + `
	DBSSL    string ` + "`mapstructure:\"DB_SSL\"`" + `
}

func LoadConfig() (*Config, error) {
	viper.SetConfigFile(".env")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
`

const databaseTemplate = `package database

import (
	"fmt"
	"log"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"{{.Project.ModuleName}}/pkg/config"
)

func InitDB(cfg *config.Config) (*gorm.DB, error) {
	// MySQL 连接字符串格式:
	// [username[:password]@][protocol[(address)]]/dbname[?param1=value1&...&paramN=valueN]
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		cfg.DBUser,
		cfg.DBPass,
		cfg.DBHost,
		cfg.DBPort,
		cfg.DBName,
	)

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	log.Println("MySQL database connection established")
	return db, nil
}
`

const serverTemplate = `package api

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"{{.Project.ModuleName}}/pkg/config"
	"{{.Project.ModuleName}}/pkg/handlers"
	"{{.Project.ModuleName}}/pkg/middlewares"
)

type Server struct {
	router *gin.Engine
	cfg    *config.Config
	db     *gorm.DB
}

func NewServer(cfg *config.Config, db *gorm.DB) *Server {
	server := &Server{
		cfg: cfg,
		db:  db,
	}
	server.setupRouter()
	return server
}

func (s *Server) setupRouter() {
	r := gin.Default()

	// 中间件
	r.Use(middlewares.LoggerMiddleware())

	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// API路由
	api := r.Group("/api/v1")
	{{range .Models}}
	handlers.Register{{.Name}}Routes(api, s.db)
	{{end}}

	s.router = r
}

func (s *Server) Run() error {
	return s.router.Run(":" + s.cfg.AppPort)
}
`

const loggerMiddlewareTemplate = `package middlewares

import (
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func LoggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		duration := time.Since(start)

		logger, _ := zap.NewProduction()
		defer logger.Sync()

		logger.Info("Request",
			zap.Int("status", c.Writer.Status()),
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("query", query),
			zap.String("ip", c.ClientIP()),
			zap.String("user-agent", c.Request.UserAgent()),
			zap.Duration("duration", duration),
		)
	}
}
`

const modelTemplate = `package models

import (
	"time"

	"gorm.io/gorm"
)

type {{.Model.Name}} struct {
	{{range .Model.Fields}}{{.Name}} {{.Type}} ` + "`{{if .GormTag}}gorm:\"{{.GormTag}}\" {{end}}json:\"{{.JsonTag}}{{if .Required}},omitempty{{end}}\"`" + `
	{{end}}CreatedAt time.Time      ` + "`json:\"created_at\"`" + `
	UpdatedAt time.Time      ` + "`json:\"updated_at\"`" + `
	DeletedAt gorm.DeletedAt ` + "`gorm:\"index\" json:\"-\"`" + `
}

func ({{.Model.Name}}) TableName() string {
	return "{{.Model.SnakeName}}"
}
`

const handlerTemplate = `package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"{{.Project.ModuleName}}/pkg/models"
)

func Register{{.Model.Name}}Routes(rg *gin.RouterGroup, db *gorm.DB) {
	{{.Model.LowerName}}Group := rg.Group("/{{.Model.PluralName}}")
	{
		{{.Model.LowerName}}Group.GET("", list{{.Model.Name}}s(db))
		{{.Model.LowerName}}Group.POST("", create{{.Model.Name}}(db))
		{{.Model.LowerName}}Group.GET("/:id", get{{.Model.Name}}(db))
		{{.Model.LowerName}}Group.PUT("/:id", update{{.Model.Name}}(db))
		{{.Model.LowerName}}Group.DELETE("/:id", delete{{.Model.Name}}(db))
	}
}

func list{{.Model.Name}}s(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var {{.Model.PluralName}} []models.{{.Model.Name}}
		if result := db.Find(&{{.Model.PluralName}}); result.Error != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
			return
		}
		c.JSON(http.StatusOK, {{.Model.PluralName}})
	}
}

func create{{.Model.Name}}(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input models.{{.Model.Name}}
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		if result := db.Create(&input); result.Error != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
			return
		}

		c.JSON(http.StatusCreated, input)
	}
}

func get{{.Model.Name}}(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
			return
		}

		var {{.Model.LowerName}} models.{{.Model.Name}}
		if result := db.First(&{{.Model.LowerName}}, id); result.Error != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "{{.Model.Name}} not found"})
			return
		}

		c.JSON(http.StatusOK, {{.Model.LowerName}})
	}
}

func update{{.Model.Name}}(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
			return
		}

		var {{.Model.LowerName}} models.{{.Model.Name}}
		if result := db.First(&{{.Model.LowerName}}, id); result.Error != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "{{.Model.Name}} not found"})
			return
		}

		if err := c.ShouldBindJSON(&{{.Model.LowerName}}); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		if result := db.Save(&{{.Model.LowerName}}); result.Error != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
			return
		}

		c.JSON(http.StatusOK, {{.Model.LowerName}})
	}
}

func delete{{.Model.Name}}(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
			return
		}

		if result := db.Delete(&models.{{.Model.Name}}{}, id); result.Error != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
			return
		}

		c.JSON(http.StatusNoContent, nil)
	}
}
`

const apiSpecTemplate = `openapi: 3.0.0
info:
  title: {{.Model.Name}} API
  version: 1.0.0
paths:
  /api/v1/{{.Model.PluralName}}:
    get:
      summary: 获取所有{{.Model.PluralName}}
      responses:
        '200':
          description: 成功
    post:
      summary: 创建新{{.Model.Name}}
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/{{.Model.Name}}'
      responses:
        '201':
          description: 创建成功
  /api/v1/{{.Model.PluralName}}/{id}:
    get:
      summary: 获取单个{{.Model.Name}}
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: integer
      responses:
        '200':
          description: 成功
    put:
      summary: 更新{{.Model.Name}}
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: integer
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/{{.Model.Name}}'
      responses:
        '200':
          description: 更新成功
    delete:
      summary: 删除{{.Model.Name}}
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: integer
      responses:
        '204':
          description: 删除成功

components:
  schemas:
    {{.Model.Name}}:
      type: object
      properties:
        id:
          type: integer
        {{range .Model.Fields}}
        {{.JsonTag}}:
          type: {{if eq .Type "string"}}string{{else if eq .Type "int"}}integer{{else if eq .Type "bool"}}boolean{{else}}string{{end}}
        {{end}}
        created_at:
          type: string
          format: date-time
        updated_at:
          type: string
          format: date-time
`

const envTemplate = `APP_PORT={{.Project.Port}}
DB_HOST=127.0.0.1
DB_PORT=3306  # MySQL 默认端口
DB_USER=root
DB_PASSWORD=your_mysql_password
DB_NAME=book
# 移除 DB_SSL 配置，因为 MySQL 不使用 sslmode
`

const goModTemplate = `module {{.Project.ModuleName}}

go 1.20

require (
	github.com/gin-gonic/gin v1.9.1
	github.com/spf13/viper v1.16.0
	gorm.io/driver/mysql v1.6.0
	gorm.io/gorm v1.25.4
)

require (
	github.com/bytedance/sonic v1.9.1 // indirect
	github.com/chenzhuoyu/base64x v0.0.0-20221115062448-fe3a3abad311 // indirect
	github.com/fsnotify/fsnotify v1.6.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.2 // indirect
	github.com/gin-contrib/sse v0.1.0 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.14.0 // indirect
	github.com/goccy/go-json v0.10.2 // indirect
	github.com/hashicorp/hcl v1.0.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20221227161230-091c0ba34f0a // indirect
	github.com/jackc/pgx/v5 v5.3.1 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/cpuid/v2 v2.2.4 // indirect
	github.com/leodido/go-urn v1.2.4 // indirect
	github.com/magiconair/properties v1.8.7 // indirect
	github.com/mattn/go-isatty v0.0.19 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/pelletier/go-toml/v2 v2.0.8 // indirect
	github.com/spf13/afero v1.9.5 // indirect
	github.com/spf13/cast v1.5.1 // indirect
	github.com/spf13/jwalterweatherman v1.1.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/subosito/gotenv v1.4.2 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.2.11 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.26.0 // indirect
	golang.org/x/arch v0.3.0 // indirect
	golang.org/x/crypto v0.12.0 // indirect
	golang.org/x/net v0.14.0 // indirect
	golang.org/x/sys v0.11.0 // indirect
	golang.org/x/text v0.12.0 // indirect
	google.golang.org/protobuf v1.30.0 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
`

const readmeTemplate = `# {{.Project.ProjectName}}

这是一个使用Gin框架生成的CRUD API项目。

## 项目结构

- **cmd/main.go**: 应用入口点
- **pkg/api**: API服务器实现
- **pkg/config**: 配置管理
- **pkg/database**: 数据库连接
- **pkg/models**: 数据模型
- **pkg/handlers**: 请求处理程序
- **pkg/middlewares**: 中间件
- **api**: OpenAPI规范文件
- **migrations**: 数据库迁移脚本
- **docs**: 文档

## 如何运行

1. 创建数据库:
   bash
   createdb {{.Project.ProjectName}}`
