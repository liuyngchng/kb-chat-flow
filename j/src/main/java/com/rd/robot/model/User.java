package com.rd.robot.model;

import java.time.LocalDateTime;

public class User {
    public static final int ROLE_NORMAL = 0;
    public static final int ROLE_ADMIN = 1;
    public static final int ROLE_AGENT = 2;
    public static final int ROLE_API = 3;

    private long uid;
    private String userName;
    private String userPwd; // bcrypt hashed, not returned to client
    private int role;
    private String note;
    private LocalDateTime pwdExpiresAt; // null = no expiry

    public long getUid() { return uid; }
    public void setUid(long uid) { this.uid = uid; }
    public String getUserName() { return userName; }
    public void setUserName(String userName) { this.userName = userName; }
    public String getUserPwd() { return userPwd; }
    public void setUserPwd(String userPwd) { this.userPwd = userPwd; }
    public int getRole() { return role; }
    public void setRole(int role) { this.role = role; }
    public String getNote() { return note; }
    public void setNote(String note) { this.note = note; }
    public LocalDateTime getPwdExpiresAt() { return pwdExpiresAt; }
    public void setPwdExpiresAt(LocalDateTime pwdExpiresAt) { this.pwdExpiresAt = pwdExpiresAt; }
}
