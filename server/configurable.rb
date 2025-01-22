module Configurable
  def self.extended(base)
    base.instance_variable_set(:@keys, {})
    base.instance_variable_set(:@aliases, {})
    base.instance_variable_set(:@variables, {})
    base.instance_variable_set(:@acts, [])
  end

  def keys
    @keys ||= {}
  end

  def key(key, command)
    keys[key] = command
  end

  def aliases
    @aliases ||= {}
  end

  def aliass(name, command = nil, &block)
    alias_object = Alias.new
    alias_object.name = name
    alias_object.command = command
    alias_object.block = block
    aliases[name] = alias_object
  end

  def variables
    @variables ||= {}
  end

  def var(key, value)
    variables[key] = value
  end

  def acts
    @acts ||= []
  end

  def act(regexp, command = nil, **options, &block)
    action = Action.new
    action.regexp = regexp
    action.command = command
    action.final = options[:final]
    action.button = options[:button]
    action.transform = options[:transform]
    action.window = options[:window]
    action.block = block
    acts.push(action)
  end

  def as_json
    {
      keys: @keys.to_a
    }
  end
end
