package globalrpc

const LUA_ACQUIRE = `
	local urls = redis.call("LRANGE", KEYS[2], 0, -1)
	if #urls == 0 then
		return nil
	end
	local index = redis.call("INCR", KEYS[1]) - 1
	local selectedUrl = urls[(index % #urls) + 1]
	local lockKey = KEYS[3] .. selectedUrl
	local lockAcquired = redis.call("SET", lockKey, ARGV[2], "NX", "EX", ARGV[1])
	if lockAcquired then
		return selectedUrl
	else
		return nil
	end
`

const LUA_RELEASE = `
	if redis.call("GET", KEYS[1]) == ARGV[1] then
		return redis.call("DEL", KEYS[1])
	end
	return 0
`

const LUA_RENEW = `
	if redis.call("GET", KEYS[1]) == ARGV[1] then
		return redis.call("EXPIRE", KEYS[1], ARGV[2])
	end
	return 0
`

const LUA_NONCE_NEXT = `
	if redis.call("EXISTS", KEYS[1]) == 0 then
		return redis.error("nonce key not seeded: " .. KEYS[1])
	end
	return redis.call("INCR", KEYS[1])
`
