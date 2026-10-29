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
