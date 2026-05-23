require 'socket'

class MudClient
  def initialize(host, port)
    @host = host
    @port = port
    @socket = nil
  end

  def income_handler(&proc)
    @income_handler = proc
  end

  def connect
    @socket = TCPSocket.new(@host, @port)
    puts "Connected to #{@host}:#{@port}"

    # Start reading and writing threads
    start_reading
    start_writing

    # Prevents the main thread from exiting
    # Not required because sinatra runner do that
    # Thread.list.each { |thread| thread.join unless thread == Thread.main }
  end

  def write(string)
    @socket.puts(string)
  end

  private

  PACKET_END = "\xff\xf9".force_encoding('ASCII-8BIT')

  def start_reading
    Thread.new do
      packet = ""
      loop do
        begin
          # Read data in chunks, allowing for incomplete lines
          # Read big chunk as possible to avoid UTF-8 symbol breaks
          while !(packet.end_with?(PACKET_END) || packet.include?(PACKET_END))
            chunk = @socket.recv(1024 * 100)
            if chunk.empty?
              puts "Connection closed by server."
              break
            end
            packet += chunk
          end

          if packet.end_with?(PACKET_END)
            current_packet, packet = packet.split(PACKET_END, 2)
          else
            current_packet, packet = packet, ""
          end

          processed_chunk = ""
          current_packet.chomp!(PACKET_END)
          current_packet.gsub!(PACKET_END, '') # TODO: can this break UTF8 chars?
          str = current_packet.force_encoding('UTF-8')
          str.chars.each do |char|
            if char.valid_encoding?
              processed_chunk << char  # Keep valid UTF-8 characters as they are
            else
              # Convert each byte in the invalid character to hex format
              char.bytes.each do |byte|
                processed_chunk << "[\\x#{byte.to_s(16).rjust(2, '0')}]"
              end
            end
          end

          print processed_chunk
          @income_handler.call(processed_chunk)
        rescue IOError, Errno::ECONNRESET => e
          puts "Error reading from server: #{e.message}."
          break
        end
      end
    end
  end

  def start_writing
    return
    # This is example of STDIN input for CLI version
    # Thread.new do
    #   loop do

    #     input = ""
    #     while (char = STDIN.getch) # Use `getch` to capture each keypress
    #       input << char
    #       break unless STDIN.ready? # Stop if no more characters are available
    #     end

    #     @socket.puts(input)
    #   end
    #   close
    # end
  end

  def close
    @socket.close if @socket
    puts "Disconnected from #{@host}:#{@port}"
  end
end
