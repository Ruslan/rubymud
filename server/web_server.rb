class WebServer
  attr_accessor :mud
  attr_reader :websockets, :game_engine

  LOG_FILE = 'log.jsonl'
  HISTORY_FILE = 'history.jsonl'

  def initialize
    @websockets = []

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
      append_to_history(payload['value']) if payload['source'] == 'input'
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
    @file_mutex.synchronize do
      @log_file ||= File.open(LOG_FILE, 'a')
      @log_file.puts(buffer.to_json)
      @log_file.flush
    end

    @log ||= []
    @log << buffer
    if @log.size > 5000
      @log.shift while @log.size > 1000
      @log_file.close
      truncate_log_file
    end
  end

  def load_log_from_file
    return unless File.exist?(LOG_FILE)
    @file_mutex.synchronize do
      @log ||= File.readlines(LOG_FILE).map { |line| JSON.parse(line) }
    end
  end

  def truncate_log_file
    @file_mutex.synchronize do
      # Read all lines from the file
      lines = File.readlines(LOG_FILE)

      # Keep only the last 1000 lines
      last_1000_lines = lines.last(1000)

      # Write back the last 1000 lines to the file
      File.open(LOG_FILE, 'w') do |file|
        file.puts(last_1000_lines)
      end
    end
  end

  def restore_log
    return unless @log
    @log[-100..-1].each do |log_item|
      broadcast({method: "output", value: log_item}.to_json)
    end
  end

  def load_history_from_file
    return unless File.exist?(HISTORY_FILE)
    @file_mutex.synchronize do
      @history ||= File.readlines(HISTORY_FILE).map { |line| JSON.parse(line) }.reverse.uniq.reverse
    end
  end

  def append_to_history(buffer)
    @file_mutex.synchronize do
      @history_file ||= File.open(HISTORY_FILE, 'a')
      @history_file.puts(buffer.to_json)
      @history_file.flush
    end

    @history ||= []
    @history << buffer
  end

  def restore_history
    return unless @history
    broadcast({method: "history", value: @history.uniq.last(100)}.to_json)
  end
end
