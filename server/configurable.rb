module Configurable
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
    aliases[name] = [command, block]
  end

  def variables
    @variables ||= {}
  end

  def var(key, value)
    variables[key] = value
  end

  class Action
    attr_accessor :regexp, :command, :final, :button, :block, :transform
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
    action.block = block
    acts.push(action)
  end

  def as_json
    {
      keys: @keys.to_a
    }
  end
end
