-- Lua half of the runtime (engine.go has tasks, wait, await, events).

-- Lua 5.2 compatibility (gopher-lua is 5.1; bit32 is in engine.go)
table.unpack = table.unpack or unpack
do
  local load51 = load
  -- load(string or function [, name, mode, env])
  function load(chunk, name, _, env)
    local f, err
    if type(chunk) == "string" then f, err = loadstring(chunk, name) else f, err = load51(chunk, name) end
    if f and env then setfenv(f, env) end
    return f, err
  end
end

-- pcall / xpcall that can wait: gopher-lua can't yield across a native pcall, so inside a task the protected call
-- runs as a nested coroutine whose waits are passed up to the scheduler.
do
  local raw_pcall, raw_xpcall = pcall, xpcall
  local create, resume, status, yield = coroutine.create, coroutine.resume, coroutine.status, coroutine.yield
  local function run(f, handler, ...)
    local co = create(f)
    task._adopt(co)
    local res = { resume(co, ...) }
    while true do
      if not res[1] then
        task._release(co)
        if handler then return false, handler(res[2]) end
        return false, res[2]
      end
      if status(co) == "dead" then
        task._release(co)
        return true, unpack(res, 2)
      end
      res = { resume(co, yield(unpack(res, 2))) } -- f waited: wait in the task, then carry on
    end
  end
  function pcall(f, ...)
    if not task.current() then return raw_pcall(f, ...) end
    return run(f, nil, ...)
  end
  function xpcall(f, handler, ...)
    if not task.current() then return raw_xpcall(f, handler, ...) end
    return run(f, handler, ...)
  end
end

-- ask(question, options, {default=, timeout=}) -> choice, "answered"|"timeout"
-- From a task: emits an "attention" event and waits for the agent's answer(id, choice). id is the event's seq.
function ask(question, options, opts)
  opts = opts or {}
  local id = events.emit("attention", {question = question, options = options, default = opts.default})
  local deadline = opts.timeout and (now() + opts.timeout * 1000)
  while true do
    local left = deadline and (deadline - now()) / 1000
    if left and left <= 0 then return opts.default, "timeout" end
    local ev = events.wait("answer", left)
    if ev == nil then return opts.default, "timeout" end
    if ev.data.id == id then return ev.data.choice, "answered" end
  end
end

-- answer(id, choice): the agent's reply to an attention event.
function answer(id, choice)
  events.emit("answer", {id = id, choice = choice})
  return true
end
