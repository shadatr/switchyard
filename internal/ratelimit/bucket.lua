-- Token bucket. KEYS[1] = bucket hash.
-- ARGV[1] capacity, ARGV[2] refill rate (tokens per ms), ARGV[3] cost,
-- ARGV[4] force (1 = apply cost unconditionally, may go negative or refund).
-- Returns {allowed (0/1), remaining tokens (floored), retry_after_ms}.
local capacity = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])
local force = ARGV[4] == "1"

local t = redis.call("TIME")
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)

local b = redis.call("HMGET", KEYS[1], "tokens", "ts")
local tokens = tonumber(b[1])
local ts = tonumber(b[2])
if tokens == nil or ts == nil then
  tokens = capacity
  ts = now
end
tokens = math.min(capacity, tokens + math.max(0, now - ts) * rate)

local allowed = 0
local retry = 0
if force then
  tokens = math.min(capacity, tokens - cost)
  allowed = 1
elseif tokens >= cost then
  tokens = tokens - cost
  allowed = 1
else
  retry = math.ceil((cost - tokens) / rate)
end

redis.call("HSET", KEYS[1], "tokens", tostring(tokens), "ts", now)
redis.call("PEXPIRE", KEYS[1], 120000)
return {allowed, math.floor(tokens), retry}
