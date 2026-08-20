# Teaching Notes

- Learner is new to `EXPLAIN`; introduce plan fields from first principles.
- Prefer a fast 500,000-row seed over a larger dataset.
- Keep all slow queries visible up front.
- Do not reveal a recommended index or completed interpretation until the
  learner submits both plan evidence and a proposal.
- Compare relative rows, buffers, sorting, and timing; avoid exact thresholds.
- Use interview-style follow-ups: “Why this order?”, “What does it cost on
  writes?”, and “When would PostgreSQL ignore it?”
