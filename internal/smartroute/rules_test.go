package smartroute

import (
	"strings"
	"testing"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/stretchr/testify/require"
)

func reqWithText(text string) *api.ChatRequest {
	return &api.ChatRequest{
		Messages: []api.Message{
			{Role: "user", Content: api.Content{Text: text}},
		},
	}
}

func TestRuleClassifier_CodeQueries(t *testing.T) {
	classifier := NewRuleClassifier()

	codeQueries := []string{
		"def binary_search(arr, target):",
		"Write a function to reverse a linked list.",
		"Create a class BankAccount with deposit and withdraw methods.",
		"import numpy as np\nx = np.array([1, 2, 3])",
		"const express = require('express');",
		"var total = 0;\nreturn total;",
		"```python\nprint('hello')\n```",
		"Please debug this null pointer exception in Java.",
		"Can you refactor this database query for better performance?",
		"Analyze the time and space complexity of merge sort.",
	}

	for _, q := range codeQueries {
		req := reqWithText(q)
		res := classifier.Classify(req, 1500)
		require.Equal(t, DecisionStrong, res.Decision, "query %q should route to strong", q)
		require.Equal(t, ReasonCode, res.Reason, "query %q reason should be rule_code", q)
	}
}

func TestRuleClassifier_MathQueries(t *testing.T) {
	classifier := NewRuleClassifier()

	mathQueries := []string{
		"Prove that the square root of 2 is irrational.",
		"Derive the quadratic formula from scratch.",
		"Calculate the probability of getting at least two heads in 5 coin flips.",
		"Explain step by step how to solve this equation.",
		"Solve for x: 3x^2 + 7x - 10 = 0.",
		"Find the integral of e^(2x) dx.",
		"How do I solve a second order differential equation?",
		"Compare and contrast utilitarianism and deontological ethics.",
		"Critique the argument presented in this essay.",
		"Prove that there are infinitely many prime numbers.",
	}

	for _, q := range mathQueries {
		req := reqWithText(q)
		res := classifier.Classify(req, 1500)
		require.Equal(t, DecisionStrong, res.Decision, "query %q should route to strong", q)
		require.Equal(t, ReasonMath, res.Reason, "query %q reason should be rule_math", q)
	}
}

func TestRuleClassifier_SimpleQueries(t *testing.T) {
	classifier := NewRuleClassifier()

	simpleQueries := []string{
		"hello",
		"Hi there!",
		"Greetings!",
		"Thanks!",
		"Thank you very much.",
		"Who are you?",
		"What is the capital of France?",
		"What is the capital of Japan?",
		"How many inches in a foot?",
		"Define serendipity.",
	}

	for _, q := range simpleQueries {
		req := reqWithText(q)
		res := classifier.Classify(req, 1500)
		require.Equal(t, DecisionCheap, res.Decision, "query %q should route to cheap", q)
		require.True(t, res.Reason == ReasonGreeting || res.Reason == ReasonSimpleFactual, "reason should be greeting or factual")
	}
}

func TestRuleClassifier_LongContext(t *testing.T) {
	classifier := NewRuleClassifier()

	// 2000 words -> ~2000 tokens > 1500 limit
	longText := strings.Repeat("word ", 2000)
	req := reqWithText(longText)
	res := classifier.Classify(req, 1500)
	require.Equal(t, DecisionStrong, res.Decision)
	require.Equal(t, ReasonLongContext, res.Reason)
}
