"""Keyword-based task classifier with LLM fallback.

Classifies task descriptions into agent types (coding, review, planning,
infrastructure, research) using configurable keyword pattern rules with
confidence scoring. Falls back to LLM (llama.cpp planner at port 8082)
when keyword confidence is below the configured threshold.

Requirements: 4.1, 4.2, 4.3, 4.4
"""

from __future__ import annotations

import logging
import re
from dataclasses import dataclass, field
from typing import Any

import httpx

from orchestrator.scheduler.interfaces import ClassificationResult
from orchestrator.state_machine.transitions import AgentType

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class PatternRule:
    """A keyword pattern rule for classification.

    Attributes:
        pattern: Compiled regex pattern to match against task descriptions.
        weight: Weight of this pattern for confidence scoring (0.0 to 1.0).
    """

    pattern: re.Pattern[str]
    weight: float


# Default keyword patterns per agent type.
# Each category has a list of PatternRule(regex, weight) entries.
# Patterns are case-insensitive.
DEFAULT_PATTERN_RULES: dict[AgentType, list[PatternRule]] = {
    AgentType.CODER: [
        PatternRule(re.compile(r"\b(implement|code|develop|program|write code)\b", re.IGNORECASE), 0.9),
        PatternRule(re.compile(r"\b(function|class|module|method|api endpoint)\b", re.IGNORECASE), 0.8),
        PatternRule(re.compile(r"\b(refactor|optimize|debug|fix bug|patch)\b", re.IGNORECASE), 0.85),
        PatternRule(re.compile(r"\b(feature|add|create|build)\b", re.IGNORECASE), 0.6),
        PatternRule(re.compile(r"\b(python|javascript|typescript|rust|go|java)\b", re.IGNORECASE), 0.7),
        PatternRule(re.compile(r"\b(test|unit test|integration test)\b", re.IGNORECASE), 0.65),
        PatternRule(re.compile(r"\b(frontend|backend|fullstack|database schema)\b", re.IGNORECASE), 0.7),
    ],
    AgentType.REVIEWER: [
        PatternRule(re.compile(r"\b(review|code review|pull request|pr review)\b", re.IGNORECASE), 0.95),
        PatternRule(re.compile(r"\b(audit|inspect|check quality|lint)\b", re.IGNORECASE), 0.8),
        PatternRule(re.compile(r"\b(feedback|suggestions|improvements)\b", re.IGNORECASE), 0.6),
        PatternRule(re.compile(r"\b(security review|vulnerability scan)\b", re.IGNORECASE), 0.85),
        PatternRule(re.compile(r"\b(approve|merge|diff)\b", re.IGNORECASE), 0.7),
    ],
    AgentType.PLANNER: [
        PatternRule(re.compile(r"\b(plan|design|architect|strategy)\b", re.IGNORECASE), 0.9),
        PatternRule(re.compile(r"\b(roadmap|milestone|epic|breakdown)\b", re.IGNORECASE), 0.85),
        PatternRule(re.compile(r"\b(requirements|specification|rfc|proposal)\b", re.IGNORECASE), 0.8),
        PatternRule(re.compile(r"\b(estimate|scope|prioritize|organize)\b", re.IGNORECASE), 0.7),
        PatternRule(re.compile(r"\b(decompose|subtask|task list)\b", re.IGNORECASE), 0.75),
    ],
    AgentType.INFRA: [
        PatternRule(re.compile(r"\b(deploy|infrastructure|devops|ci/cd)\b", re.IGNORECASE), 0.9),
        PatternRule(re.compile(r"\b(docker|kubernetes|k8s|container)\b", re.IGNORECASE), 0.85),
        PatternRule(re.compile(r"\b(ansible|terraform|cloudformation|helm)\b", re.IGNORECASE), 0.9),
        PatternRule(re.compile(r"\b(server|nginx|systemd|service)\b", re.IGNORECASE), 0.7),
        PatternRule(re.compile(r"\b(monitoring|alerting|prometheus|grafana)\b", re.IGNORECASE), 0.75),
        PatternRule(re.compile(r"\b(network|firewall|dns|ssl|tls)\b", re.IGNORECASE), 0.7),
        PatternRule(re.compile(r"\b(backup|restore|migration|scaling)\b", re.IGNORECASE), 0.65),
    ],
    AgentType.RESEARCHER: [
        PatternRule(re.compile(r"\b(research|investigate|explore|analyze)\b", re.IGNORECASE), 0.9),
        PatternRule(re.compile(r"\b(compare|evaluate|benchmark|study)\b", re.IGNORECASE), 0.8),
        PatternRule(re.compile(r"\b(documentation|summarize|explain|report)\b", re.IGNORECASE), 0.7),
        PatternRule(re.compile(r"\b(find|search|discover|look into)\b", re.IGNORECASE), 0.6),
        PatternRule(re.compile(r"\b(best practices|state of the art|alternatives)\b", re.IGNORECASE), 0.75),
    ],
}

# LLM classification prompt template
_LLM_CLASSIFICATION_PROMPT = (
    "You are a task classifier. Classify the following task into exactly one category.\n\n"
    "Categories:\n"
    "- coding: Writing, modifying, debugging, or refactoring code\n"
    "- review: Reviewing code, pull requests, or auditing quality\n"
    "- planning: Designing architecture, creating plans, breaking down work\n"
    "- infrastructure: Deployment, DevOps, server configuration, CI/CD\n"
    "- research: Investigating, analyzing, comparing options, documentation\n\n"
    "Task description: {description}\n\n"
    "Respond with ONLY the category name (one word: coding, review, planning, "
    "infrastructure, or research). Nothing else."
)

# Mapping from LLM category names to AgentType
_CATEGORY_TO_AGENT: dict[str, AgentType] = {
    "coding": AgentType.CODER,
    "review": AgentType.REVIEWER,
    "planning": AgentType.PLANNER,
    "infrastructure": AgentType.INFRA,
    "research": AgentType.RESEARCHER,
}


class KeywordClassifier:
    """Task classifier using keyword pattern matching with LLM fallback.

    Implements the IClassifier protocol. First attempts keyword-based
    classification using configurable regex pattern rules with weighted
    confidence scoring. If confidence is below the threshold, falls back
    to LLM classification via llama.cpp planner (OpenAI-compatible API).

    Args:
        pattern_rules: Mapping of agent types to their pattern rules.
            Defaults to DEFAULT_PATTERN_RULES.
        confidence_threshold: Minimum confidence for keyword classification
            before triggering LLM fallback. Default 0.8.
        llm_base_url: Base URL for the llama.cpp planner API.
            Default http://localhost:8082.
        llm_timeout: Timeout in seconds for LLM classification requests.
            Default 10.0.
    """

    def __init__(
        self,
        pattern_rules: dict[AgentType, list[PatternRule]] | None = None,
        confidence_threshold: float = 0.8,
        llm_base_url: str = "http://localhost:8082",
        llm_timeout: float = 10.0,
    ) -> None:
        self._pattern_rules = pattern_rules if pattern_rules is not None else DEFAULT_PATTERN_RULES
        self._confidence_threshold = confidence_threshold
        self._llm_base_url = llm_base_url
        self._llm_timeout = llm_timeout

    @property
    def confidence_threshold(self) -> float:
        """The configured confidence threshold for keyword classification."""
        return self._confidence_threshold

    def _score_agent_type(self, description: str, agent_type: AgentType) -> float:
        """Calculate confidence score for a specific agent type.

        Scores are computed as the weighted average of matching patterns,
        scaled by the ratio of matched patterns to total patterns.
        If no patterns match, returns 0.0.

        Args:
            description: The task description to score.
            agent_type: The agent type to score against.

        Returns:
            Confidence score between 0.0 and 1.0.
        """
        rules = self._pattern_rules.get(agent_type, [])
        if not rules:
            return 0.0

        matched_weights: list[float] = []
        for rule in rules:
            if rule.pattern.search(description):
                matched_weights.append(rule.weight)

        if not matched_weights:
            return 0.0

        # Confidence = average weight of matches * ratio of patterns matched
        match_ratio = len(matched_weights) / len(rules)
        avg_weight = sum(matched_weights) / len(matched_weights)
        return avg_weight * match_ratio

    def classify_by_keywords(self, description: str) -> ClassificationResult:
        """Classify a task using keyword pattern matching only.

        Args:
            description: The task description to classify.

        Returns:
            ClassificationResult with the best matching agent type.
        """
        scores: dict[AgentType, float] = {}
        for agent_type in AgentType:
            scores[agent_type] = self._score_agent_type(description, agent_type)

        best_agent = max(scores, key=scores.get)  # type: ignore[arg-type]
        best_score = scores[best_agent]

        return ClassificationResult(
            agent_type=best_agent,
            confidence=best_score,
            method="keyword",
        )

    async def _classify_by_llm(self, description: str) -> ClassificationResult | None:
        """Attempt classification via LLM (llama.cpp planner).

        Makes an OpenAI-compatible chat completion request to the llama.cpp
        planner instance with a 10-second timeout. Returns None if the
        request fails or times out.

        Args:
            description: The task description to classify.

        Returns:
            ClassificationResult if successful, None on failure/timeout.
        """
        prompt = _LLM_CLASSIFICATION_PROMPT.format(description=description)

        try:
            async with httpx.AsyncClient(timeout=self._llm_timeout) as client:
                response = await client.post(
                    f"{self._llm_base_url}/v1/chat/completions",
                    json={
                        "model": "planner",
                        "messages": [{"role": "user", "content": prompt}],
                        "max_tokens": 20,
                        "temperature": 0.0,
                    },
                )
                response.raise_for_status()

            data = response.json()
            content = (
                data.get("choices", [{}])[0]
                .get("message", {})
                .get("content", "")
                .strip()
                .lower()
            )

            agent_type = _CATEGORY_TO_AGENT.get(content)
            if agent_type is None:
                logger.warning(
                    "LLM returned unrecognized category: %r", content
                )
                return None

            return ClassificationResult(
                agent_type=agent_type,
                confidence=0.9,
                method="llm",
            )

        except httpx.TimeoutException:
            logger.warning(
                "LLM classification timed out after %.1fs", self._llm_timeout
            )
            return None
        except (httpx.HTTPStatusError, httpx.RequestError) as exc:
            logger.warning("LLM classification request failed: %s", exc)
            return None
        except (KeyError, IndexError, ValueError) as exc:
            logger.warning("LLM classification response parsing failed: %s", exc)
            return None

    async def classify(
        self, description: str, metadata: dict[str, Any]
    ) -> ClassificationResult:
        """Classify a task into an agent type with confidence score.

        First checks metadata for an explicit agent_type hint. Then attempts
        keyword-based classification. If confidence is below the threshold,
        falls back to LLM classification. If LLM also fails, returns the
        keyword result regardless of confidence.

        Args:
            description: The task description text.
            metadata: Additional task metadata. If it contains an "agent_type"
                key with a valid AgentType value, that is used directly.

        Returns:
            ClassificationResult with agent_type, confidence, and method.
        """
        # Check if metadata provides an explicit agent type hint
        if "agent_type" in metadata:
            try:
                agent_type = AgentType(metadata["agent_type"])
                return ClassificationResult(
                    agent_type=agent_type,
                    confidence=1.0,
                    method="metadata",
                )
            except ValueError:
                pass

        keyword_result = self.classify_by_keywords(description)

        if keyword_result.confidence >= self._confidence_threshold:
            logger.debug(
                "Keyword classification succeeded",
                extra={
                    "agent_type": keyword_result.agent_type.value,
                    "confidence": keyword_result.confidence,
                },
            )
            return keyword_result

        logger.debug(
            "Keyword confidence %.2f below threshold %.2f, trying LLM",
            keyword_result.confidence,
            self._confidence_threshold,
        )

        llm_result = await self._classify_by_llm(description)
        if llm_result is not None:
            logger.debug(
                "LLM classification succeeded",
                extra={"agent_type": llm_result.agent_type.value},
            )
            return llm_result

        # LLM failed — return keyword result (caller decides what to do
        # based on confidence being below threshold)
        logger.warning(
            "LLM fallback failed, returning keyword result with low confidence %.2f",
            keyword_result.confidence,
        )
        return keyword_result
