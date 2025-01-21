class WebServer
  attr_accessor :mud
  attr_reader :websockets, :game_engine

  def initialize
    @websockets = []
    @game_engine = GameEngine.new
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

    append_to_history(parsed_lines.map(&:as_json))

    broadcast({method: "output", value: parsed_lines.map(&:as_json)}.to_json)
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

  def append_to_history(buffer)
    @history ||= []
    @history << buffer
    @history.shift while @history.size > 100
  end

  def restore_history
    @history&.each do |history_item|
      broadcast({method: "output", value: history_item}.to_json)
    end
  end
end
