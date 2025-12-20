package gemini

// GenerateContentRequest represents a Gemini API request
type GenerateContentRequest struct {
	Contents          []*Content         `json:"contents"`
	SystemInstruction *Content           `json:"systemInstruction,omitempty"`
	Tools             []*Tool            `json:"tools,omitempty"`
	GenerationConfig  *GenerationConfig  `json:"generationConfig,omitempty"`
	SafetySettings    []*SafetySetting   `json:"safetySettings,omitempty"`
}

// Content represents a message content in Gemini format
type Content struct {
	Role  string  `json:"role"`
	Parts []*Part `json:"parts"`
}

// Part represents a part of content (text, function call, etc.)
type Part struct {
	Text             string            `json:"text,omitempty"`
	InlineData       *Blob             `json:"inlineData,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
	FileData         *FileData         `json:"fileData,omitempty"`
	VideoMetadata    *VideoMetadata    `json:"videoMetadata,omitempty"`
	
	// Thought indicates if the part is thought from the model
	Thought bool `json:"thought,omitempty"`
	
	// ThoughtSignature is an opaque signature for the thought
	ThoughtSignature string `json:"thoughtSignature,omitempty"`
}

// Blob represents inline binary data
type Blob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64 encoded
}

// FileData represents a reference to a file
type FileData struct {
	MimeType string `json:"mimeType"`
	FileURI  string `json:"fileUri"`
}

// VideoMetadata contains video-specific metadata
type VideoMetadata struct {
	StartOffset string `json:"startOffset,omitempty"`
	EndOffset   string `json:"endOffset,omitempty"`
}

// FunctionCall represents a function call from the model
type FunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args,omitempty"`
}

// FunctionResponse represents a function call result
type FunctionResponse struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

// Tool represents a tool/function definition
type Tool struct {
	FunctionDeclarations []*FunctionDeclaration `json:"functionDeclarations,omitempty"`
	CodeExecution        *CodeExecution         `json:"codeExecution,omitempty"`
}

// FunctionDeclaration describes a function that can be called
type FunctionDeclaration struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

// CodeExecution represents code execution capability
type CodeExecution struct{}

// GenerationConfig controls generation parameters
type GenerationConfig struct {
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"topP,omitempty"`
	TopK             *int            `json:"topK,omitempty"`
	CandidateCount   int             `json:"candidateCount,omitempty"`
	MaxOutputTokens  int             `json:"maxOutputTokens,omitempty"`
	StopSequences    []string        `json:"stopSequences,omitempty"`
	ResponseMimeType string          `json:"responseMimeType,omitempty"`
	ResponseSchema   *Schema         `json:"responseSchema,omitempty"`
	ResponseModalities []string      `json:"responseModalities,omitempty"`
	
	// ThinkingConfig is the thinking features configuration
	ThinkingConfig   *ThinkingConfig `json:"thinkingConfig,omitempty"`
}

// Schema for structured output
type Schema struct {
	Type       string                `json:"type"`
	Properties map[string]*Schema    `json:"properties,omitempty"`
	Items      *Schema               `json:"items,omitempty"`
	Required   []string              `json:"required,omitempty"`
	Enum       []string              `json:"enum,omitempty"`
}

// ThinkingConfig is the thinking features configuration
type ThinkingConfig struct {
	// IncludeThoughts indicates whether to include thoughts in the response
	IncludeThoughts bool `json:"includeThoughts,omitempty"`
	
	// ThinkingBudget is the thinking budget in tokens
	ThinkingBudget *int64 `json:"thinkingBudget,omitempty"`
	
	// ThinkingLevel is the level of thoughts tokens that the model should generate
	ThinkingLevel string `json:"thinkingLevel,omitempty"`
}

// SafetySetting configures content safety filtering
type SafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// GenerateContentResponse represents a Gemini API response
type GenerateContentResponse struct {
	Candidates          []*Candidate     `json:"candidates,omitempty"`
	PromptFeedback      *PromptFeedback  `json:"promptFeedback,omitempty"`
	UsageMetadata       *UsageMetadata   `json:"usageMetadata,omitempty"`
	ModelVersion        string           `json:"modelVersion,omitempty"`
}

// Candidate represents a generated response candidate
type Candidate struct {
	Content       *Content       `json:"content,omitempty"`
	FinishReason  *string        `json:"finishReason,omitempty"`
	Index         int            `json:"index"`
	SafetyRatings []*SafetyRating `json:"safetyRatings,omitempty"`
}

// SafetyRating represents content safety evaluation
type SafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
	Blocked     bool   `json:"blocked,omitempty"`
}

// PromptFeedback provides feedback on the prompt
type PromptFeedback struct {
	BlockReason   string          `json:"blockReason,omitempty"`
	SafetyRatings []*SafetyRating `json:"safetyRatings,omitempty"`
}

// UsageMetadata provides token usage information
type UsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount      int `json:"totalTokenCount"`
	
	// CachedContentTokenCount is the number of tokens in the cached content
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
	
	// ThoughtsTokenCount is the number of tokens in the model's thoughts
	ThoughtsTokenCount int `json:"thoughtsTokenCount,omitempty"`
}
