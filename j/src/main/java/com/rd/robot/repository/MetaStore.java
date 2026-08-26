package com.rd.robot.repository;

import com.rd.robot.model.*;
import java.time.LocalDateTime;
import java.util.List;
import java.util.Map;

/**
 * Metadata storage interface — supports SQLite and MySQL.
 */
public interface MetaStore {

    // ============================================================
    // VDB (knowledge base) CRUD
    // ============================================================
    long createVdb(String name, String uid, boolean isPublic);
    VdbInfo getVdbByID(long id);
    List<VdbInfo> getUserVdbs(String uid);
    List<VdbInfo> getPublicVdbs(String excludeUid);
    void deleteVdb(long id);
    void setDefaultVdb(long id, String uid);
    boolean checkVdbNameExists(String name, String uid);
    long getDefaultVdbID(String uid);

    // ============================================================
    // File CRUD
    // ============================================================
    long createFileInfo(VdbFileInfo info);
    List<VdbFileInfo> getFilesByVdbID(long vdbId);
    VdbFileInfo getFileByID(long id);
    List<VdbFileInfo> getUnprocessedFiles();
    void updateFileProgress(long id, double percent, String info);
    void deleteFile(long id);
    VdbFileInfo checkFileMD5Exists(long vdbId, String md5);

    // ============================================================
    // Prompt templates
    // ============================================================
    String getPrompt(String name);
    void upsertPrompt(String name, String value, int uid);

    // ============================================================
    // Users
    // ============================================================
    User getUserByLogin(String userName);
    User getUserByName(String userName);
    List<User> listUsers();
    void createUser(String userName, String userPwd, int role, String note);
    void deleteUserByName(String userName);
    void resetPassword(String userName, String userPwdHash);
    void updatePassword(String userName, String newPwdHash);
    void clearPwdExpiry(String userName);

    // ============================================================
    // API tokens
    // ============================================================
    void saveApiToken(String userName, String tokenPreview, LocalDateTime expiresAt);
    List<ApiToken> getUserApiTokens(String userName);

    // ============================================================
    // API call logs
    // ============================================================
    void saveApiCallLog(String userName, String apiPath, String method,
                        String reqBody, String respBody, int statusCode, String errMsg);
    List<ApiCallLog> getUserApiCallLogs(String userName);

    // ============================================================
    // Agent CRUD
    // ============================================================
    long createAgent(AgentDef a);
    AgentDef getAgent(long id);
    List<AgentDef> listAgents();
    void updateAgent(AgentDef a);
    void deleteAgent(long id);

    // ============================================================
    // Workflow CRUD
    // ============================================================
    long createWorkflow(WorkflowDef w);
    WorkflowDef getWorkflow(long id);
    List<WorkflowDef> listWorkflows();
    void updateWorkflow(WorkflowDef w);
    void deleteWorkflow(long id);

    // ============================================================
    // FAQ
    // ============================================================
    long createFaqEntry(String answer, String sourceFile);
    long createFaqQuestion(long entryId, String question, String embeddingJson);
    List<FaqEntry> getFaqEntries();
    List<FaqQuestion> getFaqQuestionsByEntryId(long entryId);
    List<FaqQuestionWithEmbedding> getAllFaqQuestionsWithEmbedding();
    void deleteFaqEntry(long id);
    void updateFaqEntry(long id, String answer);
    void deleteFaqQuestionsByEntryId(long entryId);
    void clearAllFaq();

    // ============================================================
    // System config
    // ============================================================
    String getConfig(String key);
    void setConfig(String key, String value, String description);
    Map<String, String> getAllConfigs();
    void seedDefaultConfigs();

    // ============================================================
    // Chat sessions (persistence) — TODO: 后续迁移至 Redis
    // ============================================================
    void saveChatMessage(String uid, String role, String content);
    List<ChatMessage> getChatMessages(String uid, int limit);
    void clearChatMessages(String uid);

    // ============================================================
    // Lifecycle
    // ============================================================
    void close();
}