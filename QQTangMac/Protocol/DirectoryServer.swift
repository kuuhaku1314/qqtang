import Foundation
import Network

/// Mirrors Windows `directory-http-local-ui` on 127.0.0.1:18080:
/// after the first receive, send the official 186-byte east directory packet once.
final class DirectoryServer {
    private var listener: NWListener?
    private let packet: Data
    private var address: String

    init(address: String = "127.0.0.1:18080") {
        self.address = address
        self.packet = Self.loadOfficialPacket()
    }

    func start(address: String? = nil) throws {
        if let address { self.address = address }
        let port = UInt16(self.address.split(separator: ":").last.flatMap { UInt16($0) } ?? 18080)
        let parameters = NWParameters.tcp
        parameters.allowLocalEndpointReuse = true
        listener = try NWListener(using: parameters, on: NWEndpoint.Port(rawValue: port)!)
        listener?.newConnectionHandler = { [packet] connection in
            connection.start(queue: .global(qos: .utility))
            connection.receive(minimumIncompleteLength: 1, maximumLength: 1_048_576) { _, _, _, _ in
                connection.send(content: packet, completion: .contentProcessed { _ in
                    connection.cancel()
                })
            }
        }
        listener?.start(queue: .global(qos: .utility))
    }

    func stop() {
        listener?.cancel()
        listener = nil
    }

    private static func loadOfficialPacket() -> Data {
        let names = [
            GameDataLocator.catalogRoot()?.appendingPathComponent("directory-response-east-official.bin"),
            Bundle.main.resourceURL?.appendingPathComponent("catalog/directory-response-east-official.bin")
        ]
        for url in names.compactMap({ $0 }) {
            if let data = try? Data(contentsOf: url), data.count == 186 {
                return data
            }
        }
        return Data()
    }
}
