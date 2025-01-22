require 'json'

class GameEngine::Vm
  def initialize(engine)
    @engine = engine
    @variables = engine.config.variables || {}
    @variables_file = 'variables.json'
    load_variables_from_file
  end

  attr_reader :engine, :variables

  def echo(string)
    engine.echo(:default, string)
    nil
  end

  def wecho(window, string)
    engine.echo(window, string)
    nil
  end

  def say(str, voice = 'Milena')
    Thread.new do
      IO.popen("say -v '#{voice}'", "w+") do |io|
        io.write(str)
      end
    end
  end

  # Handles undefined methods dynamically
  def method_missing(method_name, *args, &block)
    if variables.key?(method_name.to_s)
      variables[method_name.to_s]
    elsif method_name.to_s.end_with?('=')
      set_variable(method_name.to_s.chomp('='), args.first)
    else
      super # Calls the original method_missing for undefined methods
    end
  end

  # Ensures proper behavior when `respond_to?` is called
  def respond_to_missing?(method_name, include_private = false)
    variables.key?(method_name.to_s) || method_name.to_s.end_with?('=') || super
  end

  # Adds or updates a variable and writes to file
  def set_variable(key, value)
    variables[key] = value
    dump_variables_to_file
    value
  end

  private

  # Loads variables from the JSON file if it exists
  def load_variables_from_file
    if File.exist?(@variables_file)
      file_variables = JSON.parse(File.read(@variables_file))
      @variables.merge!(file_variables)
    end
    pp @variables
  rescue JSON::ParserError => e
    puts "Error parsing #{@variables_file}: #{e.message}"
  end

  # Dumps variables to the JSON file
  def dump_variables_to_file
    File.write(@variables_file, JSON.pretty_generate(variables))
  end
end
