class WebServer
  attr_accessor :mud
  attr_reader :websockets, :game_engine

  LOG_FILE = 'log.jsonl'
  HISTORY_FILE = 'history.jsonl'

  def initialize
    @websockets = []

    @storage = Storage.new

    @file_mutex = Mutex.new
    load_log_from_file
    load_history_from_file

    @game_engine = GameEngine.new
    @game_engine.on_echo do |parsed_lines|
      parsed_lines = [parsed_lines] unless parsed_lines.is_a?(Array)
      process_parsed_lines(parsed_lines)
    end
  end

  def setup(ws)
    ws.send({method: "config", value: game_engine.config.as_json }.to_json)
  end

  def setup_all
    webscokets.each do |ws|
      setup(ws)
    end
  end

  def message_from_server(string)
    parsed_lines = game_engine.transform_from_server(string)

    process_parsed_lines(parsed_lines)
  end

  def process_parsed_lines(parsed_lines)
    # Save to log
    append_to_log(parsed_lines.map(&:as_json))

    # Send to client
    broadcast({method: "output", value: parsed_lines.map(&:as_json)}.to_json)

    # Send response commands to server
    parsed_lines.each do |pline|
      pline.commands.each do |line|
        mud.write(line)
      end
    end
  end

  def broadcast(string)
    @websockets.each do |ws|
      ws.send(string)
    end
  end

  def incoming(payload)
    case payload['method']
    when 'send'
      append_to_history(payload)
      result = game_engine.parse(payload['value'])
      result.each do |line|
        mud.write(line)
      end
    else
      puts "Unknown incoming payload: #{payload['value'].to_json}"
    end
  end

  def reload
    game_engine.reload
  end

  def append_to_log(buffer)
    @storage.append_logs(buffer)

    @log ||= []
    @log << buffer
    if @log.size > 5000
      @log.shift while @log.size > 1000
      @storage.truncate_logs # Remove from sqlite to avoid db grow? Where to store full logs?
    end
  end

  def load_log_from_file
    @log = @storage.read_logs
  end

  def restore_log
    return unless @log
    @log.last(1000).each_slice(100) do |log_group|
      broadcast({ method: "output", value: log_group }.to_json)
    end
  end

  def load_history_from_file
    @history = @storage.load_history
  end

  def append_to_history(input_object)
    @history ||= []
    @storage.append_history(input_object)
    @history << input_object['value'] if input_object['source'] == 'input'
  end

  def restore_history
    return unless @history
    broadcast({method: "history", value: @history.uniq.last(100)}.to_json)
  end
end
