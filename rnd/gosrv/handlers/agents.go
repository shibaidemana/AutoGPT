package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/swiftyos/market/database"
	"github.com/swiftyos/market/models"
	"github.com/swiftyos/market/utils"
)

func GetAgents(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		logger := zap.L().With(zap.String("function", "ListAgents"))
		// Get pagination parameters from context
		page := getPageFromContext(c.Request.Context())
		pageSize := getPageSizeFromContext(c.Request.Context())

		// Get filter parameters from context
		name := getNameFromContext(c.Request.Context())
		keywords := getKeywordsFromContext(c.Request.Context())
		categories := getCategoriesFromContext(c.Request.Context())

		logger.Debug("Request parameters",
			zap.Int("page", page),
			zap.Int("pageSize", pageSize),
			zap.String("name", utils.StringOrNil(name)),
			zap.String("keywords", utils.StringOrNil(keywords)),
			zap.String("categories", utils.StringOrNil(categories)))

		agents, err := database.GetAgents(c.Request.Context(), db, page, pageSize, name, keywords, categories)
		if err != nil {
			logger.Error("Failed to fetch agents", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch agents"})
			return
		}

		c.JSON(http.StatusOK, agents)
	}
}

func SubmitAgent(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		logger := zap.L().With(zap.String("function", "SubmitAgent"))
		var request models.AddAgentRequest
		logger.Debug("Add Agent Request body", zap.Any("request", request))
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		user, exists := c.Get("user")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
			return
		}

		agent, err := database.SubmitAgent(c.Request.Context(), db, request, user)
		if err != nil {
			logger.Error("Failed to submit agent", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to submit agent"})
			return
		}

		c.JSON(http.StatusOK, agent)
	}
}

func GetAgentDetails(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		logger := zap.L().With(zap.String("function", "GetAgentDetails"))

		agentID := c.Param("id")
		logger.Debug("Agent ID", zap.String("agentID", agentID))

		if agentID == "" {
			logger.Error("Agent ID is required")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Agent ID is required"})
			return
		}

		agent, err := database.GetAgentDetails(c.Request.Context(), db, agentID)
		if err != nil {
			if err.Error() == "agent not found" {
				logger.Error("Agent not found", zap.String("agentID", agentID))
				c.JSON(http.StatusNotFound, gin.H{"error": "Agent not found"})
				return
			}
			logger.Error("Failed to fetch agent details", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch agent details"})
			return
		}

		c.JSON(http.StatusOK, agent)
	}
}

func DownloadAgent(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		logger := zap.L().With(zap.String("function", "DownloadAgent"))

		agentID := c.Param("id")
		if agentID == "" {
			logger.Error("Agent ID is required")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Agent ID is required"})
			return
		}

		agent, err := database.GetAgentDetails(c.Request.Context(), db, agentID)
		if err != nil {
			if err.Error() == "agent not found" {
				logger.Error("Agent not found", zap.String("agentID", agentID))
				c.JSON(http.StatusNotFound, gin.H{"error": "Agent not found"})
				return
			}
			logger.Error("Failed to fetch agent details", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch agent details"})
			return
		}

		err = database.IncrementDownloadCount(c.Request.Context(), db, agentID)
		if err != nil {
			logger.Error("Failed to increment download count", zap.Error(err))
			// Continue with the download even if the count update fails
		}

		c.JSON(http.StatusOK, agent)
	}
}

func DownloadAgentFile(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		logger := zap.L().With(zap.String("function", "DownloadAgentFile"))

		agentID := c.Param("id")
		if agentID == "" {
			logger.Error("Agent ID is required")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Agent ID is required"})
			return
		}

		agentFile, err := database.GetAgentFile(c.Request.Context(), db, agentID)
		if err != nil {
			if err.Error() == "agent not found" {
				logger.Error("Agent not found", zap.String("agentID", agentID))
				c.JSON(http.StatusNotFound, gin.H{"error": "Agent not found"})
				return
			}
			logger.Error("Failed to fetch agent file", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch agent file"})
			return
		}

		err = database.IncrementDownloadCount(c.Request.Context(), db, agentID)
		if err != nil {
			logger.Error("Failed to increment download count", zap.Error(err))
			// Continue with the download even if the count update fails
		}

		fileName := fmt.Sprintf("agent_%s.json", agentID)
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fileName))
		c.JSON(http.StatusOK, agentFile.Graph)
	}
}

func TopAgentsByDownloads(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		logger := zap.L().With(zap.String("function", "TopAgentsByDownloads"))
		logger.Info("Handling request for top agents by downloads")

		page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
		if err != nil || page < 1 {
			logger.Error("Invalid page number", zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid page number"})
			return
		}

		pageSize, err := strconv.Atoi(c.DefaultQuery("pageSize", "10"))
		if err != nil || pageSize < 1 {
			logger.Error("Invalid page size", zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid page size"})
			return
		}

		agents, totalCount, err := database.GetTopAgentsByDownloads(c.Request.Context(), db, page, pageSize)
		if err != nil {
			logger.Error("Failed to fetch top agents", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch top agents"})
			return
		}

		logger.Info("Successfully fetched top agents", zap.Int("count", len(agents)), zap.Int("totalCount", totalCount))
		c.JSON(http.StatusOK, gin.H{
			"agents":     agents,
			"totalCount": totalCount,
			"page":       page,
			"pageSize":   pageSize,
		})
	}
}

func GetFeaturedAgents(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		logger := zap.L().With(zap.String("function", "GetFeaturedAgents"))
		logger.Info("Handling request for featured agents")

		category := c.Query("category")
		if category == "" {
			logger.Error("Category is required")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Category is required"})
			return
		}

		page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
		if err != nil || page < 1 {
			logger.Error("Invalid page number", zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid page number"})
			return
		}

		pageSize, err := strconv.Atoi(c.DefaultQuery("pageSize", "10"))
		if err != nil || pageSize < 1 {
			logger.Error("Invalid page size", zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid page size"})
			return
		}

		agents, totalCount, err := database.GetFeaturedAgents(c.Request.Context(), db, category, page, pageSize)
		if err != nil {
			logger.Error("Failed to fetch featured agents", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch featured agents"})
			return
		}

		logger.Info("Successfully fetched featured agents", zap.Int("count", len(agents)), zap.Int("totalCount", totalCount))
		c.JSON(http.StatusOK, gin.H{
			"agents":     agents,
			"totalCount": totalCount,
			"page":       page,
			"pageSize":   pageSize,
		})
	}
}

func SearchAgents(db *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		logger := zap.L().With(zap.String("function", "Search"))
		logger.Info("Handling search request")

		query := c.Query("q")
		if query == "" {
			logger.Error("Search query is required")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Search query is required"})
			return
		}

		categories := c.QueryArray("categories")

		page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
		if err != nil || page < 1 {
			logger.Error("Invalid page number", zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid page number"})
			return
		}

		pageSize, err := strconv.Atoi(c.DefaultQuery("pageSize", "10"))
		if err != nil || pageSize < 1 {
			logger.Error("Invalid page size", zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid page size"})
			return
		}

		sortBy := c.DefaultQuery("sortBy", "rank")
		sortOrder := c.DefaultQuery("sortOrder", "DESC")

		agents, err := database.Search(c.Request.Context(), db, query, categories, page, pageSize, sortBy, sortOrder)
		if err != nil {
			logger.Error("Failed to perform search", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to perform search"})
			return
		}

		logger.Info("Successfully performed search", zap.Int("resultCount", len(agents)))
		c.JSON(http.StatusOK, gin.H{
			"agents":   agents,
			"page":     page,
			"pageSize": pageSize,
		})
	}
}
